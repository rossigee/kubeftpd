//go:build live

package ftp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// liveRoutingFixtureDir holds the kubeftpd custom resources and secrets of
// the itx-master cluster, exported with kubectl. The test writes a file
// through two sessions of the patched driver and checks that every file
// lands in the backend of its own user.
func liveRoutingFixtureDir(t *testing.T) string {
	dir := os.Getenv("LIVE_FIXTURES")
	if dir == "" {
		t.Skip("LIVE_FIXTURES not set")
	}
	return dir
}

func loadJSON(t *testing.T, path string, into any) {
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

func liveClient(t *testing.T) client.Client {
	dir := liveRoutingFixtureDir(t)

	scheme := runtime.NewScheme()
	if err := ftpv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	objects := []client.Object{}

	var users ftpv1.UserList
	loadJSON(t, filepath.Join(dir, "users.json"), &users)
	for i := range users.Items {
		objects = append(objects, &users.Items[i])
	}

	var davs ftpv1.WebDavBackendList
	loadJSON(t, filepath.Join(dir, "webdav.json"), &davs)
	for i := range davs.Items {
		backend := &davs.Items[i]
		// The family backend points at the family Odoo instance of the
		// cluster, the way the deployment does after the change.
		if backend.Name == "odoo-family-dav" {
			// The service of the family Odoo instance. Inside the cluster
			// the service is reached by its cluster local name, outside by
			// its ClusterIP.
			endpoint := os.Getenv("LIVE_DAV_ENDPOINT")
			if endpoint == "" {
				endpoint = "http://10.11.7.16:8069/.dav/webdav-scanner/1"
			}
			backend.Spec.Endpoint = endpoint
			backend.Spec.TLS = nil
		}
		objects = append(objects, backend)
	}

	var minios ftpv1.MinioBackendList
	loadJSON(t, filepath.Join(dir, "minio.json"), &minios)
	for i := range minios.Items {
		objects = append(objects, &minios.Items[i])
	}

	for _, name := range []string{
		"kubeftpd-scanner-credentials",
		"kubeftpd-webdav-credentials",
		"scanner-minio-credentials",
		"internal-ca-certs",
	} {
		var secret corev1.Secret
		loadJSON(t, filepath.Join(dir, "secret-"+name+".json"), &secret)
		s := secret
		objects = append(objects, &s)
	}

	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func TestLiveRouting(t *testing.T) {
	kubeClient := liveClient(t)
	auth := NewKubeAuth(kubeClient)
	driver := &KubeDriver{auth: auth, client: kubeClient}

	stamp := time.Now().Unix()
	minioFile := fmt.Sprintf("livecheck-minio-%d.bin", stamp)
	davFile := fmt.Sprintf("livecheck-dav-%d.bin", stamp)

	// The MinIO user.
	minioSession, err := driver.sessionFor("sess-minio", "scanner-archive", "192.0.2.10:50000")
	if err != nil {
		t.Fatalf("minio session: %v", err)
	}
	if minioSession.user.Spec.Backend.Kind != "MinioBackend" {
		t.Fatalf("minio session got backend %s", minioSession.user.Spec.Backend.Kind)
	}
	if _, err := minioSession.storage.PutFile("/"+minioFile, strings.NewReader("minio payload"), 0); err != nil {
		t.Fatalf("minio upload: %v", err)
	}

	// The WebDAV user.
	davSession, err := driver.sessionFor("sess-dav", "odoo-family", "192.0.2.11:50000")
	if err != nil {
		t.Fatalf("dav session: %v", err)
	}
	if davSession.user.Spec.Backend.Kind != "WebDavBackend" {
		t.Fatalf("dav session got backend %s", davSession.user.Spec.Backend.Kind)
	}
	if _, err := davSession.storage.PutFile("/"+davFile, strings.NewReader("dav payload"), 0); err != nil {
		t.Fatalf("dav upload: %v", err)
	}

	// Every session sees the file of its own user.
	if _, err := minioSession.storage.Stat("/" + minioFile); err != nil {
		t.Errorf("%s is missing in the MinIO bucket: %v", minioFile, err)
	}
	if _, err := davSession.storage.Stat("/" + davFile); err != nil {
		t.Errorf("%s is missing in the WebDAV collection: %v", davFile, err)
	}

	// And only that file: the file of the other user must not be in this
	// backend. Before the fix both files ended up in the same collection.
	if _, err := minioSession.storage.Stat("/" + davFile); err == nil {
		t.Errorf("%s landed in the MinIO bucket of another user", davFile)
	}
	if _, err := davSession.storage.Stat("/" + minioFile); err == nil {
		t.Errorf("%s landed in the WebDAV collection of another user", minioFile)
	}
}

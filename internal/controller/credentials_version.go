package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ObservedSecretVersionAnnotation records the version of the credentials
// secret that the connectivity test of a backend used.
//
// The credentials of a backend live in a secret, so a change of the secret
// does not change the generation of the backend. Without this annotation the
// connectivity test would keep the result of the first test forever, and a
// backend whose credentials were repaired would stay unready.
const ObservedSecretVersionAnnotation = "ftp.golder.org/observed-secret-version" // #nosec G101 -- annotation key, not a credential

// secretRef identifies the secret that holds the credentials of a backend.
type secretRef struct {
	name      string
	namespace string
}

// credentialsSecretVersion returns the resource version of the secret that
// holds the credentials of a backend.
func credentialsSecretVersion(ctx context.Context, kubeClient client.Client, namespace string, ref secretRef) (string, error) {
	if ref.name == "" {
		return "", fmt.Errorf("no credentials secret configured")
	}

	ns := ref.namespace
	if ns == "" {
		ns = namespace
	}

	secret := &corev1.Secret{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Name: ref.name, Namespace: ns}, secret); err != nil {
		return "", fmt.Errorf("failed to read credentials secret %s/%s: %w", ns, ref.name, err)
	}

	return secret.ResourceVersion, nil
}

// recordSecretVersion stores the version of the credentials secret that the
// connectivity test used in the annotation of the backend, so that the next
// reconciliation can tell whether the credentials changed since the test.
func recordSecretVersion(ctx context.Context, kubeClient client.Client, obj client.Object, version string) error {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	if annotations[ObservedSecretVersionAnnotation] == version {
		return nil
	}

	annotations[ObservedSecretVersionAnnotation] = version
	obj.SetAnnotations(annotations)

	return kubeClient.Update(ctx, obj)
}

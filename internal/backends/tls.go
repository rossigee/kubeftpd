package backends

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// buildTLSConfig constructs a *tls.Config from InsecureSkipVerify, an optional
// inline PEM bundle (caCert), and an optional Secret reference (caSecretRef).
// caSecretRef takes precedence over caCert when both are provided.
// backendNamespace is used as the default namespace for the secret lookup.
func buildTLSConfig(
	insecureSkipVerify bool,
	caCert string,
	caSecretRef *ftpv1.TLSCASecretRef,
	backendNamespace string,
	kubeClient client.Client,
) (*tls.Config, error) {
	cfg := &tls.Config{
		InsecureSkipVerify: insecureSkipVerify, // nolint:gosec
	}

	var pemBundle []byte

	switch {
	case caSecretRef != nil:
		ns := backendNamespace
		if caSecretRef.Namespace != nil && *caSecretRef.Namespace != "" {
			ns = *caSecretRef.Namespace
		}
		key := caSecretRef.Key
		if key == "" {
			key = "ca.crt"
		}
		secret := &corev1.Secret{}
		if err := kubeClient.Get(context.TODO(), client.ObjectKey{Name: caSecretRef.Name, Namespace: ns}, secret); err != nil {
			return nil, fmt.Errorf("failed to get CA secret %s/%s: %w", ns, caSecretRef.Name, err)
		}
		data, exists := secret.Data[key]
		if !exists {
			return nil, fmt.Errorf("key %q not found in CA secret %s/%s", key, ns, caSecretRef.Name)
		}
		pemBundle = data

	case caCert != "":
		pemBundle = []byte(caCert)
	}

	if len(pemBundle) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBundle) {
			return nil, fmt.Errorf("no valid PEM certificates found in CA bundle")
		}
		cfg.RootCAs = pool
	}

	return cfg, nil
}

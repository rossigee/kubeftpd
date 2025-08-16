package webhook

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// UserValidator validates User resources for security compliance
type UserValidator struct {
	Client  client.Client
	decoder *admission.Decoder
}

// Handle validates User resources
func (v *UserValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	user := &ftpv1.User{}
	err := (*v.decoder).Decode(req, user)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Validate password configuration
	if err := v.validatePasswordConfig(ctx, user); err != nil {
		return admission.Denied(err.Error())
	}

	// Validate password strength if plaintext
	if user.Spec.Password != "" {
		if err := v.validatePasswordStrength(user.Spec.Password); err != nil {
			return admission.Denied(err.Error())
		}
	}

	// Validate secret reference if used
	if user.Spec.PasswordSecret != nil {
		if err := v.validateSecretReference(ctx, user); err != nil {
			return admission.Denied(err.Error())
		}
	}

	// Check for production environment restrictions
	if err := v.validateProductionRestrictions(ctx, user); err != nil {
		return admission.Denied(err.Error())
	}

	return admission.Allowed("")
}

// validatePasswordConfig ensures proper password configuration
func (v *UserValidator) validatePasswordConfig(ctx context.Context, user *ftpv1.User) error {
	hasPassword := user.Spec.Password != ""
	hasSecret := user.Spec.PasswordSecret != nil

	if !hasPassword && !hasSecret {
		return fmt.Errorf("either password or passwordSecret must be specified")
	}

	if hasPassword && hasSecret {
		return fmt.Errorf("cannot specify both password and passwordSecret")
	}

	return nil
}

// validatePasswordStrength checks plaintext password strength
func (v *UserValidator) validatePasswordStrength(password string) error {
	// Minimum length check
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters long")
	}

	// Check for common weak patterns
	weakPatterns := []string{
		"password", "123456", "qwerty", "admin", "test", "user",
		"welcome", "login", "pass", "secret", "default",
	}

	lowercasePassword := strings.ToLower(password)
	for _, weak := range weakPatterns {
		if strings.Contains(lowercasePassword, weak) {
			return fmt.Errorf("password contains weak pattern: %s", weak)
		}
	}

	// Complexity requirements
	var hasUpper, hasLower, hasDigit, hasSpecial bool

	for _, char := range password {
		switch {
		case char >= 'A' && char <= 'Z':
			hasUpper = true
		case char >= 'a' && char <= 'z':
			hasLower = true
		case char >= '0' && char <= '9':
			hasDigit = true
		case strings.ContainsRune("!@#$%^&*()_+-=[]{}|;:,.<>?", char):
			hasSpecial = true
		}
	}

	missing := []string{}
	if !hasUpper {
		missing = append(missing, "uppercase letter")
	}
	if !hasLower {
		missing = append(missing, "lowercase letter")
	}
	if !hasDigit {
		missing = append(missing, "digit")
	}
	if !hasSpecial {
		missing = append(missing, "special character")
	}

	if len(missing) > 0 {
		return fmt.Errorf("password must contain at least one: %s", strings.Join(missing, ", "))
	}

	// Check for sequential characters
	sequential := regexp.MustCompile(`(012|123|234|345|456|567|678|789|890|abc|bcd|cde|def|efg|fgh|ghi|hij|ijk|jkl|klm|lmn|mno|nop|opq|pqr|qrs|rst|stu|tuv|uvw|vwx|wxy|xyz)`)
	if sequential.MatchString(strings.ToLower(password)) {
		return fmt.Errorf("password cannot contain sequential characters")
	}

	return nil
}

// validateSecretReference checks if secret exists and is accessible
func (v *UserValidator) validateSecretReference(ctx context.Context, user *ftpv1.User) error {
	secretRef := user.Spec.PasswordSecret
	if secretRef == nil {
		return nil
	}

	// Determine secret namespace
	secretNamespace := user.Namespace
	if secretRef.Namespace != nil && *secretRef.Namespace != "" {
		secretNamespace = *secretRef.Namespace
	}

	// Check if secret exists
	secret := &corev1.Secret{}
	err := v.Client.Get(ctx, client.ObjectKey{
		Name:      secretRef.Name,
		Namespace: secretNamespace,
	}, secret)
	if err != nil {
		return fmt.Errorf("password secret %s/%s not found or not accessible", secretNamespace, secretRef.Name)
	}

	// Check if password key exists in secret
	passwordKey := secretRef.Key
	if passwordKey == "" {
		passwordKey = "password"
	}

	if _, exists := secret.Data[passwordKey]; !exists {
		return fmt.Errorf("password key '%s' not found in secret %s/%s", passwordKey, secretNamespace, secretRef.Name)
	}

	// Validate password strength from secret
	passwordBytes := secret.Data[passwordKey]
	if len(passwordBytes) == 0 {
		return fmt.Errorf("password in secret %s/%s is empty", secretNamespace, secretRef.Name)
	}

	// Apply same strength validation to secret passwords
	if err := v.validatePasswordStrength(string(passwordBytes)); err != nil {
		return fmt.Errorf("password in secret %s/%s is weak: %v", secretNamespace, secretRef.Name, err)
	}

	return nil
}

// validateProductionRestrictions enforces production environment security policies
func (v *UserValidator) validateProductionRestrictions(ctx context.Context, user *ftpv1.User) error {
	// Check if this is a production namespace
	namespace := &corev1.Namespace{}
	err := v.Client.Get(ctx, client.ObjectKey{Name: user.Namespace}, namespace)
	if err != nil {
		// If we can't get namespace info, allow the operation
		return nil
	}

	// Check for production environment label
	isProduction := false
	if env, exists := namespace.Labels["environment"]; exists && env == "production" {
		isProduction = true
	}
	if env, exists := namespace.Labels["env"]; exists && env == "prod" {
		isProduction = true
	}

	if isProduction {
		// In production, plaintext passwords are not allowed
		if user.Spec.Password != "" {
			return fmt.Errorf("plaintext passwords are not allowed in production environments, use passwordSecret instead")
		}

		// Require stronger permissions restrictions in production
		if user.Spec.Permissions.Delete {
			// Warn but don't block - log this for monitoring
			// Could be made stricter based on requirements
		}

		// Require specific naming conventions for production secrets
		if user.Spec.PasswordSecret != nil {
			secretName := user.Spec.PasswordSecret.Name
			if !strings.HasSuffix(secretName, "-ftp-password") && !strings.HasSuffix(secretName, "-ftp-credentials") {
				return fmt.Errorf("production password secrets must follow naming convention: '*-ftp-password' or '*-ftp-credentials'")
			}
		}
	}

	return nil
}

// InjectDecoder injects the decoder
func (v *UserValidator) InjectDecoder(d *admission.Decoder) error {
	v.decoder = d
	return nil
}

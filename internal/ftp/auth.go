package ftp

import (
	"context"
	"log"
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	ftpv1 "github.com/rossigee/kubeftpd/api/v1"
)

// KubeAuth implements FTP authentication against Kubernetes User CRDs
type KubeAuth struct {
	client           client.Client
	userCache        sync.Map // Thread-safe cache for User objects: string -> *ftpv1.User
	lastAuthUser     string   // Track the last authenticated user for driver setup
	lastAuthUserLock sync.RWMutex
}

// NewKubeAuth creates a new KubeAuth instance
func NewKubeAuth(kubeClient client.Client) *KubeAuth {
	return &KubeAuth{
		client: kubeClient,
	}
}

// CheckPasswd validates user credentials against User CRDs
func (auth *KubeAuth) CheckPasswd(username, password string) (bool, error) {
	log.Printf("Authenticating user: %s", username)

	// First try to get from cache
	if cachedUser, ok := auth.userCache.Load(username); ok {
		user := cachedUser.(*ftpv1.User)
		if user.Spec.Enabled && user.Spec.Password == password {
			log.Printf("User %s authenticated from cache", username)
			auth.setLastAuthUser(username)
			return true, nil
		}
		if !user.Spec.Enabled {
			log.Printf("User %s is disabled", username)
			return false, nil
		}
		if user.Spec.Password != password {
			log.Printf("Invalid password for user %s", username)
			return false, nil
		}
	}

	// If not in cache or authentication failed, refresh from Kubernetes
	userList := &ftpv1.UserList{}
	if err := auth.client.List(context.TODO(), userList); err != nil {
		log.Printf("Failed to list users: %v", err)
		return false, err
	}

	for _, user := range userList.Items {
		if user.Spec.Username == username {
			// Update cache with fresh data
			userCopy := user.DeepCopy()
			auth.userCache.Store(username, userCopy)

			if user.Spec.Enabled && user.Spec.Password == password {
				log.Printf("User %s authenticated successfully", username)
				auth.setLastAuthUser(username)
				return true, nil
			}

			if !user.Spec.Enabled {
				log.Printf("User %s is disabled", username)
				return false, nil
			}

			log.Printf("Invalid password for user %s", username)
			return false, nil
		}
	}

	log.Printf("User %s not found", username)
	return false, nil
}

// GetUser returns a user from cache or loads from Kubernetes
func (auth *KubeAuth) GetUser(username string) *ftpv1.User {
	// Try cache first
	if cachedUser, ok := auth.userCache.Load(username); ok {
		return cachedUser.(*ftpv1.User)
	}

	// Load from Kubernetes
	userList := &ftpv1.UserList{}
	if err := auth.client.List(context.TODO(), userList); err != nil {
		log.Printf("Failed to list users while getting user %s: %v", username, err)
		return nil
	}

	for _, user := range userList.Items {
		if user.Spec.Username == username {
			userCopy := user.DeepCopy()
			auth.userCache.Store(username, userCopy)
			return userCopy
		}
	}

	return nil
}

// RefreshUserCache refreshes the user cache from Kubernetes
func (auth *KubeAuth) RefreshUserCache(ctx context.Context) error {
	log.Printf("Refreshing user cache")

	userList := &ftpv1.UserList{}
	if err := auth.client.List(ctx, userList); err != nil {
		log.Printf("Failed to refresh user cache: %v", err)
		return err
	}

	// Clear existing cache and populate with fresh data
	auth.userCache.Range(func(key, value interface{}) bool {
		auth.userCache.Delete(key)
		return true
	})

	for _, user := range userList.Items {
		userCopy := user.DeepCopy()
		auth.userCache.Store(user.Spec.Username, userCopy)
	}

	log.Printf("User cache refreshed with %d users", len(userList.Items))
	return nil
}

// StartCacheRefresh starts a background goroutine to periodically refresh the user cache
func (auth *KubeAuth) StartCacheRefresh(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("Stopping user cache refresh")
			return
		case <-ticker.C:
			if err := auth.RefreshUserCache(ctx); err != nil {
				log.Printf("Failed to refresh user cache: %v", err)
			}
		}
	}
}

// UpdateUser updates a user in the cache
func (auth *KubeAuth) UpdateUser(user *ftpv1.User) {
	if user != nil && user.Spec.Username != "" {
		userCopy := user.DeepCopy()
		auth.userCache.Store(user.Spec.Username, userCopy)
		log.Printf("Updated user %s in cache", user.Spec.Username)
	}
}

// DeleteUser removes a user from the cache
func (auth *KubeAuth) DeleteUser(username string) {
	auth.userCache.Delete(username)
	log.Printf("Deleted user %s from cache", username)
}

// setLastAuthUser safely sets the last authenticated user
func (auth *KubeAuth) setLastAuthUser(username string) {
	auth.lastAuthUserLock.Lock()
	defer auth.lastAuthUserLock.Unlock()
	auth.lastAuthUser = username
}

// GetLastAuthUser safely gets the last authenticated user
func (auth *KubeAuth) GetLastAuthUser() string {
	auth.lastAuthUserLock.RLock()
	defer auth.lastAuthUserLock.RUnlock()
	return auth.lastAuthUser
}

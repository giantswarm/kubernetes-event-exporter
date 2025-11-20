package sinks

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
)

type threadInfo struct {
	Timestamp string
	ChannelID string
}

type ThreadCache interface {
	Get(key string) (threadInfo, bool)
	Set(key string, info threadInfo) error
	Delete(key string) error
}

type InMemoryCache struct {
	store map[string]threadInfo
	mu    sync.RWMutex
}

func NewInMemoryCache() *InMemoryCache {
	return &InMemoryCache{
		store: make(map[string]threadInfo),
	}
}

func (c *InMemoryCache) Get(key string) (threadInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	info, ok := c.store[key]
	return info, ok
}

func (c *InMemoryCache) Set(key string, info threadInfo) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[key] = info
	return nil
}

func (c *InMemoryCache) Delete(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.store, key)
	return nil
}

type ConfigMapCacheConfig struct {
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
}

type ConfigMapCache struct {
	client    kubernetes.Interface
	namespace string
	name      string
	// We keep an in-memory copy for fast reads, but we should be careful about consistency.
	store map[string]threadInfo
	mu    sync.RWMutex
}

func NewConfigMapCache(cfg *ConfigMapCacheConfig) (*ConfigMapCache, error) {
	// In-cluster config
	k8sConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s client: %w", err)
	}

	c := &ConfigMapCache{
		client:    clientset,
		namespace: cfg.Namespace,
		name:      cfg.Name,
		store:     make(map[string]threadInfo),
	}

	if err := c.load(); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *ConfigMapCache) load() error {
	cm, err := c.client.CoreV1().ConfigMaps(c.namespace).Get(context.Background(), c.name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			cm = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      c.name,
					Namespace: c.namespace,
				},
				Data: map[string]string{
					"threads": "{}",
				},
			}
			_, err = c.client.CoreV1().ConfigMaps(c.namespace).Create(context.Background(), cm, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create configmap: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to get configmap: %w", err)
	}

	if data, ok := cm.Data["threads"]; ok {
		c.mu.Lock()
		defer c.mu.Unlock()
		if err := json.Unmarshal([]byte(data), &c.store); err != nil {
			log.Error().Err(err).Msg("Failed to unmarshal threads data from configmap")
			// Don't fail, just start empty? Or maybe it's corrupt.
			// We can overwrite it later.
		}
	}
	return nil
}

func (c *ConfigMapCache) save() error {
	// Retry on conflict ensures that if multiple upgrades happen simultaneously
	// (or any other concurrent modification to the ConfigMap), we don't fail.
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		c.mu.RLock()
		data, err := json.Marshal(c.store)
		c.mu.RUnlock()
		if err != nil {
			return err
		}

		cm, err := c.client.CoreV1().ConfigMaps(c.namespace).Get(context.Background(), c.name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		cm.Data["threads"] = string(data)

		_, err = c.client.CoreV1().ConfigMaps(c.namespace).Update(context.Background(), cm, metav1.UpdateOptions{})
		return err
	})
}

func (c *ConfigMapCache) Get(key string) (threadInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	info, ok := c.store[key]
	return info, ok
}

func (c *ConfigMapCache) Set(key string, info threadInfo) error {
	c.mu.Lock()
	c.store[key] = info
	c.mu.Unlock()

	return c.save()
}

func (c *ConfigMapCache) Delete(key string) error {
	c.mu.Lock()
	delete(c.store, key)
	c.mu.Unlock()

	return c.save()
}

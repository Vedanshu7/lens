// Package store defines the key naming conventions used across all persistence backends.
package store

import "fmt"

// KeyPrefix is prepended to every storage key. Override before calling New to
// avoid collisions when sharing a Redis or Valkey instance with other systems.
var KeyPrefix = "lens"

// NodeKey returns the key under which a single instance's agent URL is stored.
func NodeKey(service, instance string) string {
	return fmt.Sprintf("%s:node:%s:%s", KeyPrefix, service, instance)
}

// CacheKey returns the key under which an instance's declared cache keys are stored.
func CacheKey(service, instance string) string {
	return fmt.Sprintf("%s:cache:%s:%s", KeyPrefix, service, instance)
}

// LogKey returns the key for the replay log of a service.
func LogKey(service string) string {
	return KeyPrefix + ":log:" + service
}

// CheckpointKey returns the key that stores the last-seen timestamp for an instance.
// Used by replayMissed to determine which log entries arrived while the instance was offline.
func CheckpointKey(service, instance string) string {
	return fmt.Sprintf("%s:checkpoint:%s:%s", KeyPrefix, service, instance)
}

// AuditKey returns the key for the global audit log.
func AuditKey() string {
	return KeyPrefix + ":audit"
}

// ServiceSetKey returns the key of the set that tracks live instance names for a service.
// Discovery providers read this set to build gossip bootstrap seed lists.
func ServiceSetKey(service string) string {
	return KeyPrefix + ":service:" + service
}

// ServicesSetKey returns the key of the set containing all known service names.
func ServicesSetKey() string {
	return KeyPrefix + ":services"
}

// ProvidersKey returns the key under which a service's provider stack JSON is stored.
func ProvidersKey(service string) string {
	return fmt.Sprintf("%s:providers:%s", KeyPrefix, service)
}

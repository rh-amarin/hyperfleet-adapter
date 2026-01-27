package tasks

import (
	"fmt"
	"strings"
	"sync"
)

const (
	// TaskPrefix is the prefix for all HyperFleet custom tasks
	TaskPrefix = "hf:"

	// Task type constants
	TaskExtract       = "hf:extract"
	TaskHTTP          = "hf:http"
	TaskK8s           = "hf:k8s"
	TaskK8sRead       = "hf:k8s-read" // Reads secrets and configmaps from K8s
	TaskCEL           = "hf:cel"
	TaskTemplate      = "hf:template"
	TaskPrecondition  = "hf:precondition"
	TaskPreconditions = "hf:preconditions"
	TaskResources     = "hf:resources"
	TaskPost          = "hf:post"
)

// Registry manages custom task runner factories.
// It provides thread-safe registration and lookup of task runners.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]TaskRunnerFactory
}

// NewRegistry creates a new empty task registry.
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]TaskRunnerFactory),
	}
}

// Register adds a task runner factory to the registry.
// Returns an error if a factory is already registered for the given name.
func (r *Registry) Register(name string, factory TaskRunnerFactory) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.factories[name]; exists {
		return fmt.Errorf("task runner already registered: %s", name)
	}

	r.factories[name] = factory
	return nil
}

// MustRegister registers a task runner factory, panicking on error.
// Use this for registrations that should never fail (e.g., built-in tasks).
func (r *Registry) MustRegister(name string, factory TaskRunnerFactory) {
	if err := r.Register(name, factory); err != nil {
		panic(err)
	}
}

// Get retrieves a task runner factory by name.
// Returns the factory and true if found, nil and false otherwise.
func (r *Registry) Get(name string) (TaskRunnerFactory, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	factory, exists := r.factories[name]
	return factory, exists
}

// Create instantiates a task runner for the given task name using the provided dependencies.
// Returns an error if no factory is registered for the task name.
func (r *Registry) Create(name string, deps *Dependencies) (TaskRunner, error) {
	factory, exists := r.Get(name)
	if !exists {
		return nil, fmt.Errorf("no task runner registered for: %s", name)
	}

	return factory(deps)
}

// IsHyperFleetTask checks if the given call name is a HyperFleet custom task.
func IsHyperFleetTask(callName string) bool {
	return strings.HasPrefix(callName, TaskPrefix)
}

// ListRegistered returns a list of all registered task names.
func (r *Registry) ListRegistered() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	return names
}

// defaultRegistry is the global registry instance used for built-in tasks.
var defaultRegistry = NewRegistry()

// DefaultRegistry returns the global default registry.
func DefaultRegistry() *Registry {
	return defaultRegistry
}

// RegisterDefault registers a task runner factory in the default registry.
func RegisterDefault(name string, factory TaskRunnerFactory) error {
	return defaultRegistry.Register(name, factory)
}

// RegisterAllWithDeps registers all built-in HyperFleet tasks with the given registry.
// This is used to populate a custom registry with all available task runners.
func RegisterAllWithDeps(registry *Registry, deps *Dependencies) error {
	// List of all built-in task factories
	builtInTasks := map[string]TaskRunnerFactory{
		TaskExtract:       NewExtractTaskRunner,
		TaskHTTP:          NewHTTPTaskRunner,
		TaskK8s:           NewK8sTaskRunner,
		TaskK8sRead:       NewK8sReadTaskRunner,
		TaskCEL:           NewCELTaskRunner,
		TaskTemplate:      NewTemplateTaskRunner,
		TaskPrecondition:  NewPreconditionTaskRunner,
		TaskPreconditions: NewPreconditionsTaskRunner,
		TaskResources:     NewResourcesTaskRunner,
		TaskPost:          NewPostTaskRunner,
	}

	for name, factory := range builtInTasks {
		if err := registry.Register(name, factory); err != nil {
			return fmt.Errorf("failed to register %s: %w", name, err)
		}
	}

	return nil
}

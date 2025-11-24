package k8s_client

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestGVKFromKindAndApiVersion(t *testing.T) {
	tests := []struct {
		name       string
		kind       string
		apiVersion string
		want       schema.GroupVersionKind
		wantErr    bool
	}{
		{
			name:       "core v1 resource - Pod",
			kind:       "Pod",
			apiVersion: "v1",
			want: schema.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "Pod",
			},
			wantErr: false,
		},
		{
			name:       "core v1 resource - Namespace",
			kind:       "Namespace",
			apiVersion: "v1",
			want: schema.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "Namespace",
			},
			wantErr: false,
		},
		{
			name:       "apps resource - Deployment",
			kind:       "Deployment",
			apiVersion: "apps/v1",
			want: schema.GroupVersionKind{
				Group:   "apps",
				Version: "v1",
				Kind:    "Deployment",
			},
			wantErr: false,
		},
		{
			name:       "batch resource - Job",
			kind:       "Job",
			apiVersion: "batch/v1",
			want: schema.GroupVersionKind{
				Group:   "batch",
				Version: "v1",
				Kind:    "Job",
			},
			wantErr: false,
		},
		{
			name:       "rbac resource - ClusterRole",
			kind:       "ClusterRole",
			apiVersion: "rbac.authorization.k8s.io/v1",
			want: schema.GroupVersionKind{
				Group:   "rbac.authorization.k8s.io",
				Version: "v1",
				Kind:    "ClusterRole",
			},
			wantErr: false,
		},
		{
			name:       "custom resource",
			kind:       "MyCustomResource",
			apiVersion: "example.com/v1alpha1",
			want: schema.GroupVersionKind{
				Group:   "example.com",
				Version: "v1alpha1",
				Kind:    "MyCustomResource",
			},
			wantErr: false,
		},
		{
			name:       "apiVersion without group",
			kind:       "Pod",
			apiVersion: "invalid-format",
			want: schema.GroupVersionKind{
				Group:   "",
				Version: "invalid-format",
				Kind:    "Pod",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GVKFromKindAndApiVersion(tt.kind, tt.apiVersion)
			if (err != nil) != tt.wantErr {
				t.Errorf("GVKFromKindAndApiVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if got.Group != tt.want.Group {
					t.Errorf("GVKFromKindAndApiVersion() Group = %v, want %v", got.Group, tt.want.Group)
				}
				if got.Version != tt.want.Version {
					t.Errorf("GVKFromKindAndApiVersion() Version = %v, want %v", got.Version, tt.want.Version)
				}
				if got.Kind != tt.want.Kind {
					t.Errorf("GVKFromKindAndApiVersion() Kind = %v, want %v", got.Kind, tt.want.Kind)
				}
			}
		})
	}
}

func TestCommonResourceKinds(t *testing.T) {
	tests := []struct {
		name     string
		gvk      schema.GroupVersionKind
		wantKind string
	}{
		{
			name:     "Namespace",
			gvk:      CommonResourceKinds.Namespace,
			wantKind: "Namespace",
		},
		{
			name:     "Pod",
			gvk:      CommonResourceKinds.Pod,
			wantKind: "Pod",
		},
		{
			name:     "Service",
			gvk:      CommonResourceKinds.Service,
			wantKind: "Service",
		},
		{
			name:     "Deployment",
			gvk:      CommonResourceKinds.Deployment,
			wantKind: "Deployment",
		},
		{
			name:     "Job",
			gvk:      CommonResourceKinds.Job,
			wantKind: "Job",
		},
		{
			name:     "ConfigMap",
			gvk:      CommonResourceKinds.ConfigMap,
			wantKind: "ConfigMap",
		},
		{
			name:     "Secret",
			gvk:      CommonResourceKinds.Secret,
			wantKind: "Secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.gvk.Kind != tt.wantKind {
				t.Errorf("CommonResourceKinds.%s.Kind = %v, want %v", tt.name, tt.gvk.Kind, tt.wantKind)
			}
			// Verify it has a valid version
			if tt.gvk.Version == "" {
				t.Errorf("CommonResourceKinds.%s.Version is empty", tt.name)
			}
		})
	}
}

func TestCommonResourceKindsGroups(t *testing.T) {
	tests := []struct {
		name      string
		gvk       schema.GroupVersionKind
		wantGroup string
	}{
		{
			name:      "Core resources have empty group",
			gvk:       CommonResourceKinds.Namespace,
			wantGroup: "",
		},
		{
			name:      "Apps resources have apps group",
			gvk:       CommonResourceKinds.Deployment,
			wantGroup: "apps",
		},
		{
			name:      "Batch resources have batch group",
			gvk:       CommonResourceKinds.Job,
			wantGroup: "batch",
		},
		{
			name:      "RBAC resources have rbac group",
			gvk:       CommonResourceKinds.ClusterRole,
			wantGroup: "rbac.authorization.k8s.io",
		},
		{
			name:      "Networking resources have networking group",
			gvk:       CommonResourceKinds.Ingress,
			wantGroup: "networking.k8s.io",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.gvk.Group != tt.wantGroup {
				t.Errorf("Expected group %v, got %v", tt.wantGroup, tt.gvk.Group)
			}
		})
	}
}


package diffprocessor

import (
	"context"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/errors"
	tu "github.com/crossplane/crossplane/cmd/crank/beta/diff/testutils"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestDefaultResourceManager_FetchCurrentObject(t *testing.T) {
	ctx := context.Background()

	// Create test resources
	existingResource := tu.NewResource("example.org/v1", "TestResource", "existing-resource").
		WithSpecField("field", "value").
		Build()

	// Resource with generateName instead of name
	resourceWithGenerateName := tu.NewResource("example.org/v1", "TestResource", "").
		WithSpecField("field", "value").
		Build()
	resourceWithGenerateName.SetGenerateName("test-resource-")

	// Existing resource that matches generateName pattern
	existingGeneratedResource := tu.NewResource("example.org/v1", "TestResource", "test-resource-abc123").
		WithSpecField("field", "value").
		WithLabels(map[string]string{
			"crossplane.io/composite": "parent-xr",
		}).
		WithAnnotations(map[string]string{
			"crossplane.io/composition-resource-name": "resource-a",
		}).
		Build()

	// Existing resource that matches generateName pattern
	existingGeneratedResourceWithDifferentResName := tu.NewResource("example.org/v1", "TestResource", "test-resource-abc123").
		WithSpecField("field", "value").
		WithLabels(map[string]string{
			"crossplane.io/composite": "parent-xr",
		}).
		WithAnnotations(map[string]string{
			"crossplane.io/composition-resource-name": "resource-b",
		}).
		Build()

	// Composed resource with annotations
	composedResource := tu.NewResource("example.org/v1", "ComposedResource", "composed-resource").
		WithSpecField("field", "value").
		WithLabels(map[string]string{
			"crossplane.io/composite": "parent-xr",
		}).
		WithAnnotations(map[string]string{
			"crossplane.io/composition-resource-name": "resource-a",
		}).
		Build()

	// Parent XR
	parentXR := tu.NewResource("example.org/v1", "XR", "parent-xr").
		WithSpecField("field", "value").
		Build()

	tests := []struct {
		name           string
		setupClient    func() *tu.MockClusterClient
		composite      *unstructured.Unstructured
		desired        *unstructured.Unstructured
		wantIsNew      bool
		wantResourceID string
		wantErr        bool
	}{
		{
			name: "ExistingResourceFoundDirectly",
			setupClient: func() *tu.MockClusterClient {
				return tu.NewMockClusterClient().
					WithResourcesExist(existingResource).
					Build()
			},
			composite:      nil,
			desired:        existingResource.DeepCopy(),
			wantIsNew:      false,
			wantResourceID: "existing-resource",
			wantErr:        false,
		},
		{
			name: "ResourceNotFound",
			setupClient: func() *tu.MockClusterClient {
				return tu.NewMockClusterClient().
					WithResourceNotFound().
					Build()
			},
			composite:      nil,
			desired:        tu.NewResource("example.org/v1", "TestResource", "non-existent").Build(),
			wantIsNew:      true,
			wantResourceID: "",
			wantErr:        false,
		},
		{
			name: "CompositeIsNil_NewXR",
			setupClient: func() *tu.MockClusterClient {
				return tu.NewMockClusterClient().
					WithResourceNotFound().
					Build()
			},
			composite:      nil,
			desired:        tu.NewResource("example.org/v1", "XR", "new-xr").Build(),
			wantIsNew:      true,
			wantResourceID: "",
			wantErr:        false,
		},
		{
			name: "ResourceWithGenerateName_NotFound",
			setupClient: func() *tu.MockClusterClient {
				return tu.NewMockClusterClient().
					WithResourceNotFound().
					Build()
			},
			composite:      nil,
			desired:        resourceWithGenerateName,
			wantIsNew:      true,
			wantResourceID: "",
			wantErr:        false,
		},
		{
			name: "ResourceWithGenerateName_FoundByLabelAndAnnotation",
			setupClient: func() *tu.MockClusterClient {
				return tu.NewMockClusterClient().
					// Return "not found" for direct name lookup
					WithGetResource(func(ctx context.Context, gvk schema.GroupVersionKind, ns, name string) (*unstructured.Unstructured, error) {
						return nil, apierrors.NewNotFound(
							schema.GroupResource{
								Group:    gvk.Group,
								Resource: strings.ToLower(gvk.Kind) + "s",
							},
							name,
						)
					}).
					// Return existing resource when looking up by label
					WithGetResourcesByLabel(func(ctx context.Context, ns string, gvr schema.GroupVersionResource, sel metav1.LabelSelector) ([]*unstructured.Unstructured, error) {
						if owner, exists := sel.MatchLabels["crossplane.io/composite"]; exists && owner == "parent-xr" {
							return []*unstructured.Unstructured{existingGeneratedResource, existingGeneratedResourceWithDifferentResName}, nil
						}
						return []*unstructured.Unstructured{}, nil
					}).
					Build()
			},
			composite: parentXR,
			desired: tu.NewResource("example.org/v1", "TestResource", "").
				WithLabels(map[string]string{
					"crossplane.io/composite": "parent-xr",
				}).
				WithAnnotations(map[string]string{
					"crossplane.io/composition-resource-name": "resource-a",
				}).
				WithGenerateName("test-resource-").
				Build(),
			wantIsNew:      false,
			wantResourceID: "test-resource-abc123",
			wantErr:        false,
		},
		{
			name: "ComposedResource_FoundByLabelAndAnnotation",
			setupClient: func() *tu.MockClusterClient {
				return tu.NewMockClusterClient().
					// Return "not found" for direct name lookup
					WithResourceNotFound().
					// Return our existing resource when looking up by label
					WithResourcesFoundByLabel([]*unstructured.Unstructured{composedResource}, "crossplane.io/composite", "parent-xr").
					Build()
			},
			composite: parentXR,
			desired: tu.NewResource("example.org/v1", "ComposedResource", "composed-resource").
				WithLabels(map[string]string{
					"crossplane.io/composite": "parent-xr",
				}).
				WithAnnotations(map[string]string{
					"crossplane.io/composition-resource-name": "resource-a",
				}).
				Build(),
			wantIsNew:      false,
			wantResourceID: "composed-resource",
			wantErr:        false,
		},
		{
			name: "NoAnnotations_NewResource",
			setupClient: func() *tu.MockClusterClient {
				return tu.NewMockClusterClient().
					WithResourceNotFound().
					Build()
			},
			composite: parentXR,
			desired: tu.NewResource("example.org/v1", "Resource", "resource-name").
				WithLabels(map[string]string{
					"crossplane.io/composite": "parent-xr",
				}).
				// No composition-resource-name annotation
				Build(),
			wantIsNew:      true,
			wantResourceID: "",
			wantErr:        false,
		},
		{
			name: "GenerateNameMismatch",
			setupClient: func() *tu.MockClusterClient {
				mismatchedResource := tu.NewResource("example.org/v1", "TestResource", "different-prefix-abc123").
					WithLabels(map[string]string{
						"crossplane.io/composite": "parent-xr",
					}).
					WithAnnotations(map[string]string{
						"crossplane.io/composition-resource-name": "resource-a",
					}).
					Build()

				return tu.NewMockClusterClient().
					WithResourceNotFound().
					WithResourcesFoundByLabel([]*unstructured.Unstructured{mismatchedResource}, "crossplane.io/composite", "parent-xr").
					Build()
			},
			composite: parentXR,
			desired: tu.NewResource("example.org/v1", "TestResource", "").
				WithLabels(map[string]string{
					"crossplane.io/composite": "parent-xr",
				}).
				WithAnnotations(map[string]string{
					"crossplane.io/composition-resource-name": "resource-a",
				}).
				WithGenerateName("test-resource-").
				Build(),
			wantIsNew:      true, // Should be treated as new because generateName prefix doesn't match
			wantResourceID: "",
			wantErr:        false,
		},
		{
			name: "ErrorLookingUpResources",
			setupClient: func() *tu.MockClusterClient {
				return tu.NewMockClusterClient().
					WithResourceNotFound().
					WithGetResourcesByLabel(func(ctx context.Context, ns string, gvr schema.GroupVersionResource, sel metav1.LabelSelector) ([]*unstructured.Unstructured, error) {
						return nil, errors.New("error looking up resources")
					}).
					Build()
			},
			composite: parentXR,
			desired: tu.NewResource("example.org/v1", "ComposedResource", "").
				WithLabels(map[string]string{
					"crossplane.io/composite": "parent-xr",
				}).
				WithAnnotations(map[string]string{
					"crossplane.io/composition-resource-name": "resource-a",
				}).
				WithGenerateName("test-resource-").
				Build(),
			wantIsNew: true,  // Fall back to creating a new resource
			wantErr:   false, // We handle the error gracefully
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create the resource manager
			rm := NewResourceManager(tt.setupClient(), tu.VerboseTestLogger(t))

			// Call the method under test
			current, isNew, err := rm.FetchCurrentObject(ctx, tt.composite, tt.desired)

			// Check error expectations
			if tt.wantErr {
				if err == nil {
					t.Errorf("FetchCurrentObject() expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("FetchCurrentObject() unexpected error: %v", err)
			}

			// Check if isNew flag matches expectations
			if isNew != tt.wantIsNew {
				t.Errorf("FetchCurrentObject() isNew = %v, want %v", isNew, tt.wantIsNew)
			}

			// For new resources, current should be nil
			if isNew && current != nil {
				t.Errorf("FetchCurrentObject() returned non-nil current for new resource")
			}

			// For existing resources, check the resource ID
			if !isNew && tt.wantResourceID != "" {
				if current == nil {
					t.Fatalf("FetchCurrentObject() returned nil current for existing resource")
				}
				if current.GetName() != tt.wantResourceID {
					t.Errorf("FetchCurrentObject() current.GetName() = %v, want %v",
						current.GetName(), tt.wantResourceID)
				}
			}
		})
	}
}

func TestDefaultResourceManager_UpdateOwnerRefs(t *testing.T) {
	// Create test resources
	parentXR := tu.NewResource("example.org/v1", "XR", "parent-xr").Build()
	parentXR.SetUID("parent-uid")

	tests := []struct {
		name     string
		parent   *unstructured.Unstructured
		child    *unstructured.Unstructured
		validate func(t *testing.T, child *unstructured.Unstructured)
	}{
		{
			name:   "NilParent_NoChange",
			parent: nil,
			child: tu.NewResource("example.org/v1", "Child", "child-resource").
				WithOwnerReference("some-api-version", "SomeKind", "some-name", "foobar").
				Build(),
			validate: func(t *testing.T, child *unstructured.Unstructured) {
				// Owner refs should be unchanged
				ownerRefs := child.GetOwnerReferences()
				if len(ownerRefs) != 1 {
					t.Fatalf("Expected 1 owner reference, got %d", len(ownerRefs))
				}
				// UID should be generated but not parent's UID
				if ownerRefs[0].UID == "parent-uid" {
					t.Errorf("UID should not be parent's UID when parent is nil")
				}
				if ownerRefs[0].UID == "" {
					t.Errorf("UID should not be empty")
				}
			},
		},
		{
			name:   "MatchingOwnerRef_UpdatedWithParentUID",
			parent: parentXR,
			child: tu.NewResource("example.org/v1", "Child", "child-resource").
				WithOwnerReference("XR", "parent-xr", "example.org/v1", "").
				Build(),
			validate: func(t *testing.T, child *unstructured.Unstructured) {
				// Owner reference should be updated with parent's UID
				ownerRefs := child.GetOwnerReferences()
				if len(ownerRefs) != 1 {
					t.Fatalf("Expected 1 owner reference, got %d", len(ownerRefs))
				}
				if ownerRefs[0].UID != "parent-uid" {
					t.Errorf("UID = %s, want %s", ownerRefs[0].UID, "parent-uid")
				}
			},
		},
		{
			name:   "NonMatchingOwnerRef_GenerateRandomUID",
			parent: parentXR,
			child: tu.NewResource("example.org/v1", "Child", "child-resource").
				WithOwnerReference("other-api-version", "OtherKind", "other-name", "").
				Build(),
			validate: func(t *testing.T, child *unstructured.Unstructured) {
				// Owner reference should have a UID, but not parent's UID
				ownerRefs := child.GetOwnerReferences()
				if len(ownerRefs) != 1 {
					t.Fatalf("Expected 1 owner reference, got %d", len(ownerRefs))
				}
				if ownerRefs[0].UID == "parent-uid" {
					t.Errorf("UID should not be parent's UID for non-matching owner ref")
				}
				if ownerRefs[0].UID == "" {
					t.Errorf("UID should not be empty")
				}
			},
		},
		{
			name:   "MultipleOwnerRefs_OnlyUpdateMatching",
			parent: parentXR,
			child: func() *unstructured.Unstructured {
				child := tu.NewResource("example.org/v1", "Child", "child-resource").Build()

				// Add multiple owner references
				child.SetOwnerReferences([]metav1.OwnerReference{
					{
						APIVersion: "example.org/v1",
						Kind:       "XR",
						Name:       "parent-xr",
						UID:        "", // Empty UID should be updated
					},
					{
						APIVersion: "other.org/v1",
						Kind:       "OtherKind",
						Name:       "other-name",
						UID:        "", // Empty UID should be generated
					},
					{
						APIVersion: "example.org/v1",
						Kind:       "XR",
						Name:       "different-parent",
						UID:        "", // Empty UID should be generated
					},
				})

				return child
			}(),
			validate: func(t *testing.T, child *unstructured.Unstructured) {
				ownerRefs := child.GetOwnerReferences()
				if len(ownerRefs) != 3 {
					t.Fatalf("Expected 3 owner references, got %d", len(ownerRefs))
				}

				// Check each owner ref
				for _, ref := range ownerRefs {
					// All UIDs should be filled
					if ref.UID == "" {
						t.Errorf("UID should not be empty for any owner reference")
					}

					// Only the matching reference should have parent's UID
					if ref.APIVersion == "example.org/v1" && ref.Kind == "XR" && ref.Name == "parent-xr" {
						if ref.UID != "parent-uid" {
							t.Errorf("Matching owner ref has UID = %s, want %s", ref.UID, "parent-uid")
						}
					} else {
						if ref.UID == "parent-uid" {
							t.Errorf("Non-matching owner ref should not have parent's UID")
						}
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create the resource manager
			rm := NewResourceManager(tu.NewMockClusterClient().Build(), tu.TestLogger(t))

			// Need to create a copy of the child to avoid modifying test data
			child := tt.child.DeepCopy()

			// Call the method under test
			rm.UpdateOwnerRefs(tt.parent, child)

			// Validate the results
			tt.validate(t, child)
		})
	}
}

func TestDefaultResourceManager_FindResourcesToBeRemoved(t *testing.T) {
	ctx := context.Background()

	// Create a test XR and composed resources
	xr := tu.NewResource("example.org/v1alpha1", "XRKind", "test-xr").Build()

	resource1 := tu.NewResource("example.org/v1", "ComposedResource", "resource1").
		WithCompositeOwner("test-xr").
		WithCompositionResourceName("resource1").
		Build()

	resource2 := tu.NewResource("example.org/v1", "ComposedResource", "resource2").
		WithCompositeOwner("test-xr").
		WithCompositionResourceName("resource2").
		Build()

	tests := []struct {
		name               string
		setupClient        func() *tu.MockClusterClient
		composite          string
		processedResources map[string]bool
		wantResourceCount  int
		wantResourceNames  []string
		wantErr            bool
	}{
		{
			name: "NoResourcesToRemove",
			setupClient: func() *tu.MockClusterClient {
				return tu.NewMockClusterClient().
					WithResourcesExist(xr).
					WithResourceTreeFromXRAndComposed(xr, []*unstructured.Unstructured{resource1, resource2}).
					Build()
			},
			composite: "test-xr",
			processedResources: map[string]bool{
				"example.org/v1/ComposedResource/resource1": true,
				"example.org/v1/ComposedResource/resource2": true,
			},
			wantResourceCount: 0,
			wantErr:           false,
		},
		{
			name: "OneResourceToRemove",
			setupClient: func() *tu.MockClusterClient {
				return tu.NewMockClusterClient().
					WithResourcesExist(xr).
					WithResourceTreeFromXRAndComposed(xr, []*unstructured.Unstructured{resource1, resource2}).
					Build()
			},
			composite: "test-xr",
			processedResources: map[string]bool{
				"example.org/v1/ComposedResource/resource1": true,
				// resource2 is not in the processed list, so it should be removed
			},
			wantResourceCount: 1,
			wantResourceNames: []string{"resource2"},
			wantErr:           false,
		},
		{
			name: "AllResourcesToRemove",
			setupClient: func() *tu.MockClusterClient {
				return tu.NewMockClusterClient().
					WithResourcesExist(xr).
					WithResourceTreeFromXRAndComposed(xr, []*unstructured.Unstructured{resource1, resource2}).
					Build()
			},
			composite:          "test-xr",
			processedResources: map[string]bool{}, // No resources processed
			wantResourceCount:  2,
			wantResourceNames:  []string{"resource1", "resource2"},
			wantErr:            false,
		},
		{
			name: "ResourceTreeError",
			setupClient: func() *tu.MockClusterClient {
				return tu.NewMockClusterClient().
					WithResourcesExist(xr).
					WithFailedResourceTreeFetch("failed to get resource tree").
					Build()
			},
			composite:          "test-xr",
			processedResources: map[string]bool{},
			wantResourceCount:  0,
			wantErr:            true,
		},
		{
			name: "XRNotFound",
			setupClient: func() *tu.MockClusterClient {
				return tu.NewMockClusterClient().
					WithResourceNotFound().
					Build()
			},
			composite:          "test-xr",
			processedResources: map[string]bool{},
			wantResourceCount:  0,
			wantErr:            true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create the resource manager
			rm := NewResourceManager(tt.setupClient(), tu.TestLogger(t))

			// Call the method under test
			removed, err := rm.FindResourcesToBeRemoved(ctx, tt.composite, tt.processedResources)

			// Check error expectations
			if tt.wantErr {
				if err == nil {
					t.Errorf("FindResourcesToBeRemoved() expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("FindResourcesToBeRemoved() unexpected error: %v", err)
			}

			// Check the number of resources found
			if len(removed) != tt.wantResourceCount {
				t.Errorf("FindResourcesToBeRemoved() returned %d resources, want %d",
					len(removed), tt.wantResourceCount)
			}

			// Check that the expected resource names are in the result
			if tt.wantResourceNames != nil {
				foundNames := make([]string, 0, len(removed))
				for _, res := range removed {
					foundNames = append(foundNames, res.GetName())
				}

				// Check each expected name
				for _, name := range tt.wantResourceNames {
					found := false
					for _, foundName := range foundNames {
						if foundName == name {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("Expected resource %q to be marked for removal but it wasn't", name)
					}
				}
			}
		})
	}
}

// TestResourceKey tests the resourceKey helper function
func TestResourceKey(t *testing.T) {
	tests := []struct {
		name     string
		resource *unstructured.Unstructured
		want     string
	}{
		{
			name: "StandardResource",
			resource: tu.NewResource("example.org/v1", "TestResource", "test-resource").
				Build(),
			want: "example.org/v1/TestResource/test-resource",
		},
		{
			name: "NamespacedResource",
			resource: func() *unstructured.Unstructured {
				res := tu.NewResource("example.org/v1", "TestResource", "test-resource").
					Build()
				res.SetNamespace("test-namespace")
				return res
			}(),
			want: "example.org/v1/TestResource/test-resource",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resourceKey(tt.resource)
			if got != tt.want {
				t.Errorf("resourceKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

package clusterclient

import (
	"context"
	tu "github.com/crossplane/crossplane/cmd/crank/beta/diff/testutils"
	"github.com/crossplane/crossplane/cmd/crank/beta/internal/resource"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/errors"
	apiextensionsv1 "github.com/crossplane/crossplane/apis/apiextensions/v1"
	pkgv1 "github.com/crossplane/crossplane/apis/pkg/v1"
	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/fake"

	kt "k8s.io/client-go/testing"
)

// Ensure MockClusterClient implements the ClusterClient interface.
var _ ClusterClient = &tu.MockClusterClient{}

func TestClusterClient_GetEnvironmentConfigs(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pkgv1.AddToScheme(scheme)
	_ = apiextensionsv1.AddToScheme(scheme)

	type args struct {
		ctx context.Context
	}

	type want struct {
		envConfigs []*unstructured.Unstructured
		err        error
	}

	tests := map[string]struct {
		reason string
		setup  func() dynamic.Interface
		args   args
		want   want
	}{
		"NoConfigs": {
			reason: "Should return empty list when no configs exist",
			setup: func() dynamic.Interface {
				dc := fake.NewSimpleDynamicClientWithCustomListKinds(scheme,
					map[schema.GroupVersionResource]string{
						{Group: "apiextensions.crossplane.io", Version: "v1alpha1", Resource: "environmentconfigs"}: "EnvironmentConfigList",
					})
				return dc
			},
			args: args{
				ctx: context.Background(),
			},
			want: want{
				envConfigs: []*unstructured.Unstructured{},
			},
		},
		"AllConfigs": {
			reason: "Should return all configs when they exist",
			setup: func() dynamic.Interface {
				// Use resource builders here
				objects := []runtime.Object{
					tu.NewResource("apiextensions.crossplane.io/v1alpha1", "EnvironmentConfig", "config1").
						WithSpecField("data", map[string]interface{}{
							"key": "value1",
						}).
						Build(),
					tu.NewResource("apiextensions.crossplane.io/v1alpha1", "EnvironmentConfig", "config2").
						WithSpecField("data", map[string]interface{}{
							"key": "value2",
						}).
						Build(),
				}
				return fake.NewSimpleDynamicClient(scheme, objects...)
			},
			args: args{
				ctx: context.Background(),
			},
			want: want{
				envConfigs: []*unstructured.Unstructured{
					tu.NewResource("apiextensions.crossplane.io/v1alpha1", "EnvironmentConfig", "config1").
						WithSpecField("data", map[string]interface{}{
							"key": "value1",
						}).
						Build(),
					tu.NewResource("apiextensions.crossplane.io/v1alpha1", "EnvironmentConfig", "config2").
						WithSpecField("data", map[string]interface{}{
							"key": "value2",
						}).
						Build(),
				},
			},
		},
		"APIError": {
			reason: "Should propagate errors from the Kubernetes API",
			setup: func() dynamic.Interface {
				dc := fake.NewSimpleDynamicClientWithCustomListKinds(scheme,
					map[schema.GroupVersionResource]string{
						{Group: "apiextensions.crossplane.io", Version: "v1alpha1", Resource: "environmentconfigs"}: "EnvironmentConfigList",
					})

				dc.Fake.PrependReactor("list", "environmentconfigs", func(action kt.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("api server down")
				})

				return dc
			},
			args: args{
				ctx: context.Background(),
			},
			want: want{
				envConfigs: nil,
				err:        errors.Wrap(errors.New("api server down"), "cannot list environment configs"),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			c := &DefaultClusterClient{
				dynamicClient: tc.setup(),
				logger:        tu.TestLogger(t),
			}

			got, err := c.GetEnvironmentConfigs(tc.args.ctx)

			if tc.want.err != nil {
				if diff := cmp.Diff(tc.want.err.Error(), err.Error()); diff != "" {
					t.Errorf("\n%s\nGetEnvironmentConfigs(...): -want error, +got error:\n%s", tc.reason, diff)
				}
				return
			}

			if err != nil {
				t.Errorf("\n%s\nGetEnvironmentConfigs(...): unexpected error: %v", tc.reason, err)
				return
			}

			if diff := cmp.Diff(tc.want.envConfigs, got); diff != "" {
				t.Errorf("\n%s\nGetEnvironmentConfigs(...): -want, +got:\n%s", tc.reason, diff)
			}
		})
	}
}

func TestClusterClient_Initialize(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pkgv1.AddToScheme(scheme)
	_ = apiextensionsv1.AddToScheme(scheme)

	type args struct {
		ctx context.Context
	}

	type want struct {
		compositions map[string]*apiextensionsv1.Composition
		functions    map[string]pkgv1.Function
		err          error
		// XRD cache state
		xrdsLoaded bool
	}

	cases := map[string]struct {
		reason string
		setup  func() dynamic.Interface
		args   args
		want   want
	}{
		"NoCompositionsOrFunctions": {
			reason: "Should initialize with empty maps when no resources exist",
			setup: func() dynamic.Interface {
				return fake.NewSimpleDynamicClient(scheme)
			},
			args: args{
				ctx: context.Background(),
			},
			want: want{
				compositions: map[string]*apiextensionsv1.Composition{},
				functions:    map[string]pkgv1.Function{},
			},
		},
		"WithCompositionsAndFunctions": {
			reason: "Should initialize with compositions and functions when they exist",
			setup: func() dynamic.Interface {
				objects := []runtime.Object{
					// Use resource builders for composition and function
					tu.NewResource("apiextensions.crossplane.io/v1", "Composition", "comp1").
						WithSpecField("compositeTypeRef", map[string]interface{}{
							"apiVersion": "example.org/v1",
							"kind":       "XR1",
						}).
						Build(),
					tu.NewResource("pkg.crossplane.io/v1", "Function", "func1").Build(),
				}
				return fake.NewSimpleDynamicClient(scheme, objects...)
			},
			args: args{
				ctx: context.Background(),
			},
			want: want{
				compositions: map[string]*apiextensionsv1.Composition{
					"comp1": {
						TypeMeta: metav1.TypeMeta{
							APIVersion: "apiextensions.crossplane.io/v1",
							Kind:       "Composition",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "comp1",
						},
						Spec: apiextensionsv1.CompositionSpec{
							CompositeTypeRef: apiextensionsv1.TypeReference{
								APIVersion: "example.org/v1",
								Kind:       "XR1",
							},
						},
					},
				},
				functions: map[string]pkgv1.Function{
					"func1": {
						TypeMeta: metav1.TypeMeta{
							APIVersion: "pkg.crossplane.io/v1",
							Kind:       "Function",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "func1",
						},
					},
				},
			},
		},
		"CompositionListError": {
			reason: "Should propagate errors from composition listing",
			setup: func() dynamic.Interface {
				dc := fake.NewSimpleDynamicClientWithCustomListKinds(scheme,
					map[schema.GroupVersionResource]string{
						{Group: "apiextensions.crossplane.io", Version: "v1", Resource: "compositions"}: "CompositionList",
						{Group: "pkg.crossplane.io", Version: "v1", Resource: "functions"}:              "FunctionList",
					})

				dc.Fake.PrependReactor("list", "compositions", func(action kt.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("composition list error")
				})

				return dc
			},
			args: args{
				ctx: context.Background(),
			},
			want: want{
				err: errors.Wrap(errors.Wrap(errors.New("composition list error"), "cannot list compositions from cluster"), "cannot list compositions"),
			},
		},
		"FunctionListError": {
			reason: "Should propagate errors from function listing",
			setup: func() dynamic.Interface {
				dc := fake.NewSimpleDynamicClientWithCustomListKinds(scheme,
					map[schema.GroupVersionResource]string{
						{Group: "apiextensions.crossplane.io", Version: "v1", Resource: "compositions"}: "CompositionList",
						{Group: "pkg.crossplane.io", Version: "v1", Resource: "functions"}:              "FunctionList",
					})

				// Setup compositions to respond normally
				objects := []runtime.Object{
					tu.NewResource("apiextensions.crossplane.io/v1", "Composition", "comp1").
						WithSpecField("compositeTypeRef", map[string]interface{}{
							"apiVersion": "example.org/v1",
							"kind":       "XR1",
						}).
						Build(),
				}

				dc = fake.NewSimpleDynamicClient(scheme, objects...)

				// But make functions fail
				dc.Fake.PrependReactor("list", "functions", func(action kt.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("function list error")
				})

				return dc
			},
			args: args{
				ctx: context.Background(),
			},
			want: want{
				err: errors.Wrap(errors.Wrap(errors.New("function list error"), "cannot list functions from cluster"), "cannot list functions"),
			},
		},
		"PreloadsXRDs": {
			reason: "Should preload XRDs during initialization",
			setup: func() dynamic.Interface {
				// Create test XRDs
				xrd1 := tu.NewResource("apiextensions.crossplane.io/v1", "CompositeResourceDefinition", "xrd1").
					WithSpecField("group", "example.org").
					WithSpecField("names", map[string]interface{}{
						"kind":     "XR1",
						"plural":   "xr1s",
						"singular": "xr1",
					}).
					Build()

				// Setup dynamic client to return compositions, functions, and XRDs
				objects := []runtime.Object{
					// Composition
					tu.NewResource("apiextensions.crossplane.io/v1", "Composition", "comp1").
						WithSpecField("compositeTypeRef", map[string]interface{}{
							"apiVersion": "example.org/v1",
							"kind":       "XR1",
						}).
						Build(),
					// Function
					tu.NewResource("pkg.crossplane.io/v1", "Function", "func1").Build(),
					// XRD
					xrd1,
				}

				return fake.NewSimpleDynamicClient(scheme, objects...)
			},
			args: args{
				ctx: context.Background(),
			},
			want: want{
				compositions: map[string]*apiextensionsv1.Composition{
					"comp1": {
						TypeMeta: metav1.TypeMeta{
							APIVersion: "apiextensions.crossplane.io/v1",
							Kind:       "Composition",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "comp1",
						},
						Spec: apiextensionsv1.CompositionSpec{
							CompositeTypeRef: apiextensionsv1.TypeReference{
								APIVersion: "example.org/v1",
								Kind:       "XR1",
							},
						},
					},
				},
				functions: map[string]pkgv1.Function{
					"func1": {
						TypeMeta: metav1.TypeMeta{
							APIVersion: "pkg.crossplane.io/v1",
							Kind:       "Function",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "func1",
						},
					},
				},
				err:        nil,
				xrdsLoaded: true, // XRDs should be loaded after initialization
			},
		},
		"XRDsFetchError": {
			reason: "Should fail initialization when XRDs cannot be loaded",
			setup: func() dynamic.Interface {
				// Set up dynamic client to return compositions and functions successfully
				objects := []runtime.Object{
					// Add a test composition
					tu.NewResource("apiextensions.crossplane.io/v1", "Composition", "test-comp").
						WithSpecField("compositeTypeRef", map[string]interface{}{
							"apiVersion": "example.org/v1",
							"kind":       "XR1",
						}).
						Build(),
					// Add a test function
					tu.NewResource("pkg.crossplane.io/v1", "Function", "test-func").Build(),
				}

				dc := fake.NewSimpleDynamicClient(scheme, objects...)

				// But make XRD fetch fail
				dc.Fake.PrependReactor("list", "compositeresourcedefinitions", func(action kt.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("failed to fetch XRDs")
				})

				return dc
			},
			args: args{
				ctx: context.Background(),
			},
			want: want{
				err: errors.Wrap(errors.Wrap(errors.New("failed to fetch XRDs"), "cannot list XRDs"), "Failed to preload XRDs"),
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := &DefaultClusterClient{
				dynamicClient: tc.setup(),
				logger:        tu.TestLogger(t),
			}

			err := c.Initialize(tc.args.ctx)

			if tc.want.err != nil {
				if err == nil {
					t.Errorf("\n%s\nInitialize(...): expected error but got none", tc.reason)
					return
				}

				if diff := cmp.Diff(tc.want.err.Error(), err.Error()); diff != "" {
					t.Errorf("\n%s\nInitialize(...): -want error, +got error:\n%s", tc.reason, diff)
				}
				return
			}

			if err != nil {
				t.Errorf("\n%s\nInitialize(...): unexpected error: %v", tc.reason, err)
				return
			}

			// Check if XRDs were loaded if expected
			if tc.want.xrdsLoaded && !c.xrdsLoaded {
				t.Errorf("\n%s\nInitialize(...): expected XRDs to be loaded, but they weren't", tc.reason)
			}

			if diff := cmp.Diff(len(tc.want.compositions), len(c.compositions)); diff != "" {
				t.Errorf("\n%s\nInitialize(...): -want composition count, +got composition count:\n%s", tc.reason, diff)
			}

			for name, wantComp := range tc.want.compositions {
				gotComp, ok := c.compositions[name]
				if !ok {
					t.Errorf("\n%s\nInitialize(...): missing composition with name %s", tc.reason, name)
					continue
				}

				if diff := cmp.Diff(wantComp.Spec.CompositeTypeRef, gotComp.Spec.CompositeTypeRef); diff != "" {
					t.Errorf("\n%s\nInitialize(...): -want composition, +got composition:\n%s", tc.reason, diff)
				}
			}

			if diff := cmp.Diff(len(tc.want.functions), len(c.functions)); diff != "" {
				t.Errorf("\n%s\nInitialize(...): -want function count, +got function count:\n%s", tc.reason, diff)
			}

			for name, wantFunc := range tc.want.functions {
				gotFunc, ok := c.functions[name]
				if !ok {
					t.Errorf("\n%s\nInitialize(...): missing function with name %s", tc.reason, name)
					continue
				}

				if diff := cmp.Diff(wantFunc.GetName(), gotFunc.GetName()); diff != "" {
					t.Errorf("\n%s\nInitialize(...): -want function, +got function:\n%s", tc.reason, diff)
				}
			}
		})
	}
}

func TestClusterClient_GetAllResourcesByLabels(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pkgv1.AddToScheme(scheme)
	_ = apiextensionsv1.AddToScheme(scheme)

	type args struct {
		ctx       context.Context
		gvks      []schema.GroupVersionKind
		selectors []metav1.LabelSelector
	}

	type want struct {
		resources []*unstructured.Unstructured
		err       error
	}

	cases := map[string]struct {
		reason string
		setup  func() dynamic.Interface
		args   args
		want   want
	}{
		"MismatchedGVKsAndSelectors": {
			reason: "Should return error when GVKs and selectors count mismatch",
			setup: func() dynamic.Interface {
				return fake.NewSimpleDynamicClient(scheme)
			},
			args: args{
				ctx: context.Background(),
				gvks: []schema.GroupVersionKind{
					{Group: "example.org", Version: "v1", Kind: "Resource"},
				},
				selectors: []metav1.LabelSelector{},
			},
			want: want{
				err: errors.New("number of GVKs must match number of selectors"),
			},
		},
		"NoMatchingResources": {
			reason: "Should return empty list when no resources match selector",
			setup: func() dynamic.Interface {
				c := fake.NewSimpleDynamicClientWithCustomListKinds(scheme,
					map[schema.GroupVersionResource]string{
						{Group: "example.org", Version: "v1", Resource: "resources"}: "ResourceList",
					})
				return c
			},
			args: args{
				ctx: context.Background(),
				gvks: []schema.GroupVersionKind{
					{Group: "example.org", Version: "v1", Kind: "Resource"},
				},
				selectors: []metav1.LabelSelector{
					{
						MatchLabels: map[string]string{"app": "test"},
					},
				},
			},
			want: want{
				resources: []*unstructured.Unstructured{},
			},
		},
		"MatchingResources": {
			reason: "Should return resources matching selector",
			setup: func() dynamic.Interface {
				objects := []runtime.Object{
					// Use resource builders for the test objects
					tu.NewResource("example.org/v1", "Resource", "res1").
						WithLabels(map[string]string{
							"app": "test",
							"env": "dev",
						}).
						Build(),
					tu.NewResource("example.org/v1", "Resource", "res2").
						WithLabels(map[string]string{
							"app": "other",
						}).
						Build(),
					tu.NewResource("example.org/v2", "OtherResource", "other1").
						WithLabels(map[string]string{
							"type": "test",
						}).
						Build(),
				}
				return fake.NewSimpleDynamicClient(scheme, objects...)
			},
			args: args{
				ctx: context.Background(),
				gvks: []schema.GroupVersionKind{
					{Group: "example.org", Version: "v1", Kind: "Resource"},
					{Group: "example.org", Version: "v2", Kind: "OtherResource"},
				},
				selectors: []metav1.LabelSelector{
					{
						MatchLabels: map[string]string{"app": "test"},
					},
					{
						MatchLabels: map[string]string{"type": "test"},
					},
				},
			},
			want: want{
				resources: []*unstructured.Unstructured{
					tu.NewResource("example.org/v1", "Resource", "res1").
						WithLabels(map[string]string{
							"app": "test",
							"env": "dev",
						}).
						Build(),
					tu.NewResource("example.org/v2", "OtherResource", "other1").
						WithLabels(map[string]string{
							"type": "test",
						}).
						Build(),
				},
			},
		},
		"ListError": {
			reason: "Should propagate errors from the Kubernetes API",
			setup: func() dynamic.Interface {
				dc := fake.NewSimpleDynamicClientWithCustomListKinds(scheme,
					map[schema.GroupVersionResource]string{
						{Group: "example.org", Version: "v1", Resource: "resources"}: "ResourceList",
					})
				dc.Fake.PrependReactor("list", "resources", func(action kt.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("list error")
				})
				return dc
			},
			args: args{
				ctx: context.Background(),
				gvks: []schema.GroupVersionKind{
					{Group: "example.org", Version: "v1", Kind: "Resource"},
				},
				selectors: []metav1.LabelSelector{
					{
						MatchLabels: map[string]string{"app": "test"},
					},
				},
			},
			want: want{
				err: errors.Wrap(errors.Wrapf(errors.New("list error"),
					"cannot list resources for '%s' matching '%s'",
					"example.org/v1, Kind=Resource", "app=test"),
					"cannot get all resources"),
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := &DefaultClusterClient{
				dynamicClient: tc.setup(),
				logger:        tu.TestLogger(t),
				// Add GVK to GVR mappings for testing
				gvkToGVRMap: map[schema.GroupVersionKind]schema.GroupVersionResource{
					{Group: "example.org", Version: "v1", Kind: "Resource"}:      {Group: "example.org", Version: "v1", Resource: "resources"},
					{Group: "example.org", Version: "v2", Kind: "OtherResource"}: {Group: "example.org", Version: "v2", Resource: "otherresources"},
				},
			}

			got, err := c.GetAllResourcesByLabels(tc.args.ctx, tc.args.gvks, tc.args.selectors)

			if tc.want.err != nil {
				if err == nil {
					t.Errorf("\n%s\nGetAllResourcesByLabels(...): expected error but got none", tc.reason)
					return
				}

				if diff := cmp.Diff(tc.want.err.Error(), err.Error()); diff != "" {
					t.Errorf("\n%s\nGetAllResourcesByLabels(...): -want error, +got error:\n%s", tc.reason, diff)
				}
				return
			}

			if err != nil {
				t.Errorf("\n%s\nGetAllResourcesByLabels(...): unexpected error: %v", tc.reason, err)
				return
			}

			if diff := cmp.Diff(len(tc.want.resources), len(got)); diff != "" {
				t.Errorf("\n%s\nGetAllResourcesByLabels(...): -want resource count, +got resource count:\n%s", tc.reason, diff)
			}

			// For successful cases, compare results
			for i, wantResource := range tc.want.resources {
				if i >= len(got) {
					break
				}

				if diff := cmp.Diff(wantResource.GetName(), got[i].GetName()); diff != "" {
					t.Errorf("\n%s\nGetAllResourcesByLabels(...): -want resource name, +got resource name at index %d:\n%s", tc.reason, i, diff)
				}

				if diff := cmp.Diff(wantResource.GetLabels(), got[i].GetLabels()); diff != "" {
					t.Errorf("\n%s\nGetAllResourcesByLabels(...): -want resource labels, +got resource labels at index %d:\n%s", tc.reason, i, diff)
				}
			}
		})
	}
}

func TestClusterClient_FindMatchingComposition(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pkgv1.AddToScheme(scheme)
	_ = apiextensionsv1.AddToScheme(scheme)

	type fields struct {
		compositions map[string]*apiextensionsv1.Composition
		xrds         []*unstructured.Unstructured
	}

	type args struct {
		res *unstructured.Unstructured
	}

	type want struct {
		composition *apiextensionsv1.Composition
		err         error
	}

	// Create test compositions
	matchingComp := tu.NewComposition("matching-comp").
		WithCompositeTypeRef("example.org/v1", "XR1").
		Build()

	nonMatchingComp := tu.NewComposition("non-matching-comp").
		WithCompositeTypeRef("example.org/v1", "OtherXR").
		Build()

	referencedComp := tu.NewComposition("referenced-comp").
		WithCompositeTypeRef("example.org/v1", "XR1").
		Build()

	incompatibleComp := tu.NewComposition("incompatible-comp").
		WithCompositeTypeRef("example.org/v1", "OtherXR").
		Build()

	labeledComp := func() *apiextensionsv1.Composition {
		comp := tu.NewComposition("labeled-comp").
			WithCompositeTypeRef("example.org/v1", "XR1").
			Build()
		comp.SetLabels(map[string]string{
			"environment": "production",
			"tier":        "standard",
		})
		return comp
	}()

	aComp := func() *apiextensionsv1.Composition {
		comp := tu.NewComposition("a-comp").
			WithCompositeTypeRef("example.org/v1", "XR1").
			Build()
		comp.SetLabels(map[string]string{
			"environment": "production",
		})
		return comp
	}()

	bComp := func() *apiextensionsv1.Composition {
		comp := tu.NewComposition("b-comp").
			WithCompositeTypeRef("example.org/v1", "XR1").
			Build()
		comp.SetLabels(map[string]string{
			"environment": "production",
		})
		return comp
	}()

	versionMismatchComp := tu.NewComposition("version-mismatch-comp").
		WithCompositeTypeRef("example.org/v2", "XR1").
		Build()

	tests := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"NoMatchingComposition": {
			reason: "Should return error when no matching composition exists",
			fields: fields{
				compositions: map[string]*apiextensionsv1.Composition{
					"non-matching-comp": nonMatchingComp,
				},
			},
			args: args{
				res: tu.NewResource("example.org/v1", "XR1", "my-xr").Build(),
			},
			want: want{
				err: errors.Errorf("no composition found for %s", "example.org/v1, Kind=XR1"),
			},
		},
		"MatchingComposition": {
			reason: "Should return the matching composition",
			fields: fields{
				compositions: map[string]*apiextensionsv1.Composition{
					"matching-comp":     matchingComp,
					"non-matching-comp": nonMatchingComp,
				},
			},
			args: args{
				res: tu.NewResource("example.org/v1", "XR1", "my-xr").Build(),
			},
			want: want{
				composition: matchingComp,
			},
		},
		"DirectCompositionReference": {
			reason: "Should return the composition referenced by spec.compositionRef.name",
			fields: fields{
				compositions: map[string]*apiextensionsv1.Composition{
					"default-comp":    matchingComp,
					"referenced-comp": referencedComp,
				},
			},
			args: args{
				res: func() *unstructured.Unstructured {
					xr := tu.NewResource("example.org/v1", "XR1", "my-xr").Build()
					_ = unstructured.SetNestedField(xr.Object, "referenced-comp", "spec", "compositionRef", "name")
					return xr
				}(),
			},
			want: want{
				composition: referencedComp,
			},
		},
		"DirectCompositionReferenceIncompatible": {
			reason: "Should return error when directly referenced composition is incompatible",
			fields: fields{
				compositions: map[string]*apiextensionsv1.Composition{
					"matching-comp":     matchingComp,
					"incompatible-comp": incompatibleComp,
				},
			},
			args: args{
				res: func() *unstructured.Unstructured {
					xr := tu.NewResource("example.org/v1", "XR1", "my-xr").Build()
					_ = unstructured.SetNestedField(xr.Object, "incompatible-comp", "spec", "compositionRef", "name")
					return xr
				}(),
			},
			want: want{
				err: errors.Errorf("composition incompatible-comp is not compatible with example.org/v1, Kind=XR1"),
			},
		},
		"ReferencedCompositionNotFound": {
			reason: "Should return error when referenced composition doesn't exist",
			fields: fields{
				compositions: map[string]*apiextensionsv1.Composition{
					"existing-comp": matchingComp,
				},
			},
			args: args{
				res: func() *unstructured.Unstructured {
					xr := tu.NewResource("example.org/v1", "XR1", "my-xr").Build()
					_ = unstructured.SetNestedField(xr.Object, "non-existent-comp", "spec", "compositionRef", "name")
					return xr
				}(),
			},
			want: want{
				err: errors.Errorf("composition non-existent-comp referenced in example.org/v1, Kind=XR1/my-xr not found"),
			},
		},
		"CompositionSelectorMatch": {
			reason: "Should return composition matching the selector labels",
			fields: fields{
				compositions: map[string]*apiextensionsv1.Composition{
					"labeled-comp":      labeledComp,
					"non-matching-comp": nonMatchingComp,
				},
			},
			args: args{
				res: func() *unstructured.Unstructured {
					xr := tu.NewResource("example.org/v1", "XR1", "my-xr").Build()
					_ = unstructured.SetNestedStringMap(xr.Object, map[string]string{
						"environment": "production",
					}, "spec", "compositionSelector", "matchLabels")
					return xr
				}(),
			},
			want: want{
				composition: labeledComp,
			},
		},
		"CompositionSelectorNoMatch": {
			reason: "Should return error when no composition matches the selector",
			fields: fields{
				compositions: map[string]*apiextensionsv1.Composition{
					"labeled-comp": func() *apiextensionsv1.Composition {
						comp := tu.NewComposition("labeled-comp").
							WithCompositeTypeRef("example.org/v1", "XR1").
							Build()
						comp.SetLabels(map[string]string{
							"environment": "staging",
						})
						return comp
					}(),
				},
			},
			args: args{
				res: func() *unstructured.Unstructured {
					xr := tu.NewResource("example.org/v1", "XR1", "my-xr").Build()
					_ = unstructured.SetNestedStringMap(xr.Object, map[string]string{
						"environment": "production",
					}, "spec", "compositionSelector", "matchLabels")
					return xr
				}(),
			},
			want: want{
				err: errors.Errorf("no compatible composition found matching labels map[environment:production] for example.org/v1, Kind=XR1/my-xr"),
			},
		},
		"MultipleCompositionMatches": {
			reason: "Should return an error when multiple compositions match the selector",
			fields: fields{
				compositions: map[string]*apiextensionsv1.Composition{
					"a-comp": aComp,
					"b-comp": bComp,
				},
			},
			args: args{
				res: func() *unstructured.Unstructured {
					xr := tu.NewResource("example.org/v1", "XR1", "my-xr").Build()
					_ = unstructured.SetNestedStringMap(xr.Object, map[string]string{
						"environment": "production",
					}, "spec", "compositionSelector", "matchLabels")
					return xr
				}(),
			},
			want: want{
				err: errors.New("ambiguous composition selection: multiple compositions match"),
			},
		},
		"EmptyCompositionCache_DefaultLookup": {
			reason: "Should return error when composition cache is empty (default lookup)",
			fields: fields{
				compositions: map[string]*apiextensionsv1.Composition{},
			},
			args: args{
				res: tu.NewResource("example.org/v1", "XR1", "my-xr").Build(),
			},
			want: want{
				err: errors.Errorf("no composition found for %s", "example.org/v1, Kind=XR1"),
			},
		},
		"EmptyCompositionCache_DirectReference": {
			reason: "Should return error when composition cache is empty (direct reference)",
			fields: fields{
				compositions: map[string]*apiextensionsv1.Composition{},
			},
			args: args{
				res: func() *unstructured.Unstructured {
					xr := tu.NewResource("example.org/v1", "XR1", "my-xr").Build()
					_ = unstructured.SetNestedField(xr.Object, "referenced-comp", "spec", "compositionRef", "name")
					return xr
				}(),
			},
			want: want{
				err: errors.Errorf("composition referenced-comp referenced in example.org/v1, Kind=XR1/my-xr not found"),
			},
		},
		"EmptyCompositionCache_Selector": {
			reason: "Should return error when composition cache is empty (selector)",
			fields: fields{
				compositions: map[string]*apiextensionsv1.Composition{},
			},
			args: args{
				res: func() *unstructured.Unstructured {
					xr := tu.NewResource("example.org/v1", "XR1", "my-xr").Build()
					_ = unstructured.SetNestedStringMap(xr.Object, map[string]string{
						"environment": "production",
					}, "spec", "compositionSelector", "matchLabels")
					return xr
				}(),
			},
			want: want{
				err: errors.Errorf("no compatible composition found matching labels map[environment:production] for example.org/v1, Kind=XR1/my-xr"),
			},
		},
		"AmbiguousDefaultSelection": {
			reason: "Should return error when multiple compositions match by type but no selection criteria provided",
			fields: fields{
				compositions: map[string]*apiextensionsv1.Composition{
					"comp1": matchingComp,
					"comp2": referencedComp, // Both match same XR type
				},
			},
			args: args{
				res: tu.NewResource("example.org/v1", "XR1", "my-xr").Build(),
			},
			want: want{
				err: errors.New("ambiguous composition selection: multiple compositions exist for example.org/v1, Kind=XR1"),
			},
		},
		"DifferentVersions": {
			reason: "Should not match compositions with different versions",
			fields: fields{
				compositions: map[string]*apiextensionsv1.Composition{
					"version-mismatch-comp": versionMismatchComp,
				},
			},
			args: args{
				res: tu.NewResource("example.org/v1", "XR1", "my-xr").Build(),
			},
			want: want{
				err: errors.Errorf("no composition found for %s", "example.org/v1, Kind=XR1"),
			},
		},
		"ClaimResource": {
			reason: "Should find composition for a claim type by determining XR type from XRD",
			fields: fields{
				compositions: map[string]*apiextensionsv1.Composition{
					"matching-comp": {
						ObjectMeta: metav1.ObjectMeta{
							Name: "matching-comp",
						},
						Spec: apiextensionsv1.CompositionSpec{
							CompositeTypeRef: apiextensionsv1.TypeReference{
								APIVersion: "example.org/v2", // Match the referenceable version v2
								Kind:       "XExampleResource",
							},
						},
					},
				},
				xrds: []*unstructured.Unstructured{
					{
						Object: map[string]interface{}{
							"apiVersion": "apiextensions.crossplane.io/v1",
							"kind":       "CompositeResourceDefinition",
							"metadata": map[string]interface{}{
								"name": "xexampleresources.example.org",
							},
							"spec": map[string]interface{}{
								"group": "example.org",
								"names": map[string]interface{}{
									"kind": "XExampleResource",
								},
								"claimNames": map[string]interface{}{
									"kind": "ExampleResourceClaim",
								},
								"versions": []interface{}{
									map[string]interface{}{
										"name":          "v1",
										"served":        true,
										"referenceable": false,
									},
									map[string]interface{}{
										"name":          "v2",
										"served":        true,
										"referenceable": true, // This is the version compositions should reference
									},
									map[string]interface{}{
										"name":          "v3alpha1",
										"served":        true,
										"referenceable": false,
									},
								},
							},
						},
					},
				},
			},
			args: args{
				res: tu.NewResource("example.org/v1", "ExampleResourceClaim", "test-claim").
					WithSpecField("compositionRef", map[string]interface{}{
						"name": "matching-comp",
					}).
					Build(),
			},
			want: want{
				composition: &apiextensionsv1.Composition{
					ObjectMeta: metav1.ObjectMeta{
						Name: "matching-comp",
					},
					Spec: apiextensionsv1.CompositionSpec{
						CompositeTypeRef: apiextensionsv1.TypeReference{
							APIVersion: "example.org/v2",
							Kind:       "XExampleResource",
						},
					},
				},
				err: nil,
			},
		},
		"ClaimResourceWithNoReferenceableVersion": {
			reason: "Should return error when XRD has no referenceable version",
			fields: fields{
				compositions: map[string]*apiextensionsv1.Composition{
					"matching-comp": {
						ObjectMeta: metav1.ObjectMeta{
							Name: "matching-comp",
						},
						Spec: apiextensionsv1.CompositionSpec{
							CompositeTypeRef: apiextensionsv1.TypeReference{
								APIVersion: "example.org/v1",
								Kind:       "XExampleResource",
							},
						},
					},
				},
				xrds: []*unstructured.Unstructured{
					{
						Object: map[string]interface{}{
							"apiVersion": "apiextensions.crossplane.io/v1",
							"kind":       "CompositeResourceDefinition",
							"metadata": map[string]interface{}{
								"name": "xexampleresources.example.org",
							},
							"spec": map[string]interface{}{
								"group": "example.org",
								"names": map[string]interface{}{
									"kind": "XExampleResource",
								},
								"claimNames": map[string]interface{}{
									"kind": "ExampleResourceClaim",
								},
								"versions": []interface{}{
									map[string]interface{}{
										"name":          "v1",
										"served":        true,
										"referenceable": false, // No referenceable version
									},
									map[string]interface{}{
										"name":          "v2",
										"served":        true,
										"referenceable": false, // No referenceable version
									},
								},
							},
						},
					},
				},
			},
			args: args{
				res: tu.NewResource("example.org/v1", "ExampleResourceClaim", "test-claim").
					WithSpecField("compositionRef", map[string]interface{}{
						"name": "matching-comp",
					}).
					Build(),
			},
			want: want{
				composition: nil,
				err:         errors.New("no referenceable version found in XRD"), // Should fail with this error
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {

			// Set up fake dynamic client with XRDs
			fakeDynamicClient := fake.NewSimpleDynamicClient(scheme)
			fakeDynamicClient.PrependReactor("list", "compositeresourcedefinitions", func(action kt.Action) (bool, runtime.Object, error) {
				unstructuredList := &unstructured.UnstructuredList{}
				if tc.fields.xrds != nil {
					for _, xrd := range tc.fields.xrds {
						unstructuredList.Items = append(unstructuredList.Items, *xrd)
					}
				}
				return true, unstructuredList, nil
			})

			c := &DefaultClusterClient{
				compositions:  tc.fields.compositions,
				logger:        tu.TestLogger(t),
				dynamicClient: fakeDynamicClient,
				gvkToGVRMap:   make(map[schema.GroupVersionKind]schema.GroupVersionResource),
			}

			got, err := c.FindMatchingComposition(tc.args.res)

			if tc.want.err != nil {
				if err == nil {
					t.Errorf("\n%s\nFindMatchingComposition(...): expected error but got none", tc.reason)
					return
				}

				if !strings.Contains(err.Error(), tc.want.err.Error()) {
					t.Errorf("\n%s\nFindMatchingComposition(...): expected error containing %q, got %q",
						tc.reason, tc.want.err.Error(), err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("\n%s\nFindMatchingComposition(...): unexpected error: %v", tc.reason, err)
				return
			}

			if tc.want.composition != nil {
				if diff := cmp.Diff(tc.want.composition.Name, got.Name); diff != "" {
					t.Errorf("\n%s\nFindMatchingComposition(...): -want composition name, +got composition name:\n%s", tc.reason, diff)
				}

				if diff := cmp.Diff(tc.want.composition.Spec.CompositeTypeRef, got.Spec.CompositeTypeRef); diff != "" {
					t.Errorf("\n%s\nFindMatchingComposition(...): -want composition type ref, +got composition type ref:\n%s", tc.reason, diff)
				}
			}
		})
	}
}

func TestClusterClient_GetFunctionsFromPipeline(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pkgv1.AddToScheme(scheme)
	_ = apiextensionsv1.AddToScheme(scheme)

	pipelineMode := apiextensionsv1.CompositionModePipeline
	nonPipelineMode := apiextensionsv1.CompositionMode("NonPipeline")

	type fields struct {
		functions map[string]pkgv1.Function
	}

	type args struct {
		comp *apiextensionsv1.Composition
	}

	type want struct {
		functions []pkgv1.Function
		err       error
	}

	tests := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"NonPipelineMode": {
			reason: "Should throw an error when composition is not in pipeline mode",
			fields: fields{
				functions: map[string]pkgv1.Function{},
			},
			args: args{
				comp: &apiextensionsv1.Composition{
					Spec: apiextensionsv1.CompositionSpec{
						Mode: &nonPipelineMode,
					},
				},
			},
			want: want{
				err: errors.New("Unsupported composition Mode 'NonPipeline'; supported types are [Pipeline]"),
			},
		},
		"NoModeSpecified": {
			reason: "Should throw an error when composition mode is not specified",
			fields: fields{
				functions: map[string]pkgv1.Function{},
			},
			args: args{
				comp: &apiextensionsv1.Composition{
					Spec: apiextensionsv1.CompositionSpec{
						Mode: nil,
					},
				},
			},
			want: want{
				err: errors.New("Unsupported Composition; no Mode found."),
			},
		},
		"EmptyPipeline": {
			reason: "Should return empty slice for empty pipeline",
			fields: fields{
				functions: map[string]pkgv1.Function{},
			},
			args: args{
				comp: &apiextensionsv1.Composition{
					Spec: apiextensionsv1.CompositionSpec{
						Mode:     &pipelineMode,
						Pipeline: []apiextensionsv1.PipelineStep{},
					},
				},
			},
			want: want{
				functions: []pkgv1.Function{},
			},
		},
		"MissingFunction": {
			reason: "Should return error when a function is missing",
			fields: fields{
				functions: map[string]pkgv1.Function{
					"function-a": {
						TypeMeta: metav1.TypeMeta{
							APIVersion: "pkg.crossplane.io/v1",
							Kind:       "Function",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "function-a",
						},
					},
				},
			},
			args: args{
				comp: &apiextensionsv1.Composition{
					Spec: apiextensionsv1.CompositionSpec{
						Mode: &pipelineMode,
						Pipeline: []apiextensionsv1.PipelineStep{
							{
								Step:        "step-a",
								FunctionRef: apiextensionsv1.FunctionReference{Name: "function-a"},
							},
							{
								Step:        "step-b",
								FunctionRef: apiextensionsv1.FunctionReference{Name: "function-b"},
							},
						},
					},
				},
			},
			want: want{
				err: errors.Errorf("function %q referenced in pipeline step %q not found", "function-b", "step-b"),
			},
		},
		"AllFunctionsFound": {
			reason: "Should return all functions referenced in the pipeline",
			fields: fields{
				functions: map[string]pkgv1.Function{
					"function-a": {
						TypeMeta: metav1.TypeMeta{
							APIVersion: "pkg.crossplane.io/v1",
							Kind:       "Function",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "function-a",
						},
					},
					"function-b": {
						TypeMeta: metav1.TypeMeta{
							APIVersion: "pkg.crossplane.io/v1",
							Kind:       "Function",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "function-b",
						},
					},
				},
			},
			args: args{
				// Use Composition builder to create a composition with pipeline steps
				comp: tu.NewComposition("test-comp").
					WithPipelineMode().
					WithPipelineStep("step-a", "function-a", nil).
					WithPipelineStep("step-b", "function-b", nil).
					Build(),
			},
			want: want{
				functions: []pkgv1.Function{
					{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "pkg.crossplane.io/v1",
							Kind:       "Function",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "function-a",
						},
					},
					{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "pkg.crossplane.io/v1",
							Kind:       "Function",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "function-b",
						},
					},
				},
			},
		},
		"DuplicateFunctionRefs": {
			reason: "Should handle pipeline steps that reference the same function",
			fields: fields{
				functions: map[string]pkgv1.Function{
					"function-a": {
						TypeMeta: metav1.TypeMeta{
							APIVersion: "pkg.crossplane.io/v1",
							Kind:       "Function",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "function-a",
						},
					},
				},
			},
			args: args{
				// Use Composition builder for a composition with duplicate function references
				comp: tu.NewComposition("test-comp").
					WithPipelineMode().
					WithPipelineStep("step-a", "function-a", nil).
					WithPipelineStep("step-b", "function-a", nil).
					Build(),
			},
			want: want{
				functions: []pkgv1.Function{
					{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "pkg.crossplane.io/v1",
							Kind:       "Function",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "function-a",
						},
					},
					{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "pkg.crossplane.io/v1",
							Kind:       "Function",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "function-a",
						},
					},
				},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			c := &DefaultClusterClient{
				functions: tc.fields.functions,
				logger:    tu.TestLogger(t),
			}

			got, err := c.GetFunctionsFromPipeline(tc.args.comp)

			if tc.want.err != nil {
				if err == nil {
					t.Errorf("\n%s\nGetFunctionsFromPipeline(...): expected error but got none", tc.reason)
					return
				}

				if diff := cmp.Diff(tc.want.err.Error(), err.Error()); diff != "" {
					t.Errorf("\n%s\nGetFunctionsFromPipeline(...): -want error, +got error:\n%s", tc.reason, diff)
				}
				return
			}

			if err != nil {
				t.Errorf("\n%s\nGetFunctionsFromPipeline(...): unexpected error: %v", tc.reason, err)
				return
			}

			if tc.want.functions == nil {
				if got != nil {
					t.Errorf("\n%s\nGetFunctionsFromPipeline(...): expected nil functions, got %v", tc.reason, got)
				}
				return
			}

			if diff := cmp.Diff(len(tc.want.functions), len(got)); diff != "" {
				t.Errorf("\n%s\nGetFunctionsFromPipeline(...): -want function count, +got function count:\n%s", tc.reason, diff)
			}

			// Check each function matches what we expect
			for i, wantFn := range tc.want.functions {
				if i >= len(got) {
					break
				}
				if diff := cmp.Diff(wantFn.GetName(), got[i].GetName()); diff != "" {
					t.Errorf("\n%s\nGetFunctionsFromPipeline(...): -want function name, +got function name at index %d:\n%s", tc.reason, i, diff)
				}
			}
		})
	}
}

// Modify the existing TestClusterClient_GetXRDs to add caching test cases
func TestClusterClient_GetXRDs(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pkgv1.AddToScheme(scheme)
	_ = apiextensionsv1.AddToScheme(scheme)

	type args struct {
		ctx context.Context
	}

	type want struct {
		xrds []*unstructured.Unstructured
		err  error
	}

	// Create test XRDs for our test cases
	xrd1 := tu.NewResource("apiextensions.crossplane.io/v1", "CompositeResourceDefinition", "xrd1").
		WithSpecField("group", "example.org").
		WithSpecField("names", map[string]interface{}{
			"kind":     "XR1",
			"plural":   "xr1s",
			"singular": "xr1",
		}).
		Build()

	xrd2 := tu.NewResource("apiextensions.crossplane.io/v1", "CompositeResourceDefinition", "xrd2").
		WithSpecField("group", "example.org").
		WithSpecField("names", map[string]interface{}{
			"kind":     "XR2",
			"plural":   "xr2s",
			"singular": "xr2",
		}).
		Build()

	tests := map[string]struct {
		reason string
		setup  func() dynamic.Interface
		args   args
		want   want
		// for caching tests
		secondCall   bool // Whether to make a second call to GetXRDs
		apiCallCount int  // Expected API call count after test
	}{
		"NoXRDsFound": {
			reason: "Should return empty slice when no XRDs exist",
			setup: func() dynamic.Interface {
				dc := fake.NewSimpleDynamicClientWithCustomListKinds(scheme,
					map[schema.GroupVersionResource]string{
						{Group: "apiextensions.crossplane.io", Version: "v1", Resource: "compositeresourcedefinitions"}: "CompositeResourceDefinitionList",
					})
				return dc
			},
			args: args{
				ctx: context.Background(),
			},
			want: want{
				xrds: []*unstructured.Unstructured{},
			},
		},
		"XRDsExist": {
			reason: "Should return all XRDs when they exist",
			setup: func() dynamic.Interface {
				objects := []runtime.Object{
					// Use resource builders for XRDs
					xrd1, xrd2,
				}
				return fake.NewSimpleDynamicClient(scheme, objects...)
			},
			args: args{
				ctx: context.Background(),
			},
			want: want{
				xrds: []*unstructured.Unstructured{
					xrd1, xrd2,
				},
			},
		},
		"ListError": {
			reason: "Should propagate errors from the Kubernetes API",
			setup: func() dynamic.Interface {
				dc := fake.NewSimpleDynamicClientWithCustomListKinds(scheme,
					map[schema.GroupVersionResource]string{
						{Group: "apiextensions.crossplane.io", Version: "v1", Resource: "compositeresourcedefinitions"}: "CompositeResourceDefinitionList",
					})

				dc.Fake.PrependReactor("list", "compositeresourcedefinitions", func(action kt.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("list error")
				})

				return dc
			},
			args: args{
				ctx: context.Background(),
			},
			want: want{
				err: errors.Wrap(errors.New("list error"), "cannot list XRDs"),
			},
		},

		// Add new test cases for caching
		"CachesXRDsCorrectly": {
			reason: "Should cache XRDs after first call and not make additional API requests",
			setup: func() dynamic.Interface {
				// Setup dynamic client to return our XRDs
				objects := []runtime.Object{xrd1, xrd2}
				dc := fake.NewSimpleDynamicClient(scheme, objects...)

				// *Important*: Create a list reactor that can be tracked
				// We must use PrependReactor instead of just counting total actions at the end
				// because the list happens inside the GetXRDs method
				dc.Fake.PrependReactor("list", "compositeresourcedefinitions", func(action kt.Action) (bool, runtime.Object, error) {
					// Don't handle the reaction, just pass through
					return false, nil, nil
				})

				return dc
			},
			args: args{
				ctx: context.Background(),
			},
			want: want{
				xrds: []*unstructured.Unstructured{xrd1, xrd2},
				err:  nil,
			},
			secondCall:   true, // Make a second call to test caching
			apiCallCount: 1,    // Should only make one API call despite two GetXRDs calls
		},
		"EmptyCacheNoError": {
			reason: "Should cache empty XRD list without error",
			setup: func() dynamic.Interface {
				// Return empty list, but prepare a reactor to track the call
				dc := fake.NewSimpleDynamicClient(scheme)

				// Prepend a reactor to track API calls
				dc.Fake.PrependReactor("list", "compositeresourcedefinitions", func(action kt.Action) (bool, runtime.Object, error) {
					// Don't handle the reaction, just pass through
					return false, nil, nil
				})

				return dc
			},
			args: args{
				ctx: context.Background(),
			},
			want: want{
				xrds: []*unstructured.Unstructured{},
				err:  nil,
			},
			secondCall:   true, // Make a second call to test caching
			apiCallCount: 1,    // Should only make one API call despite two GetXRDs calls
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Create client setup
			dc := tc.setup()

			c := &DefaultClusterClient{
				dynamicClient: dc,
				logger:        tu.TestLogger(t),
			}

			// First call to GetXRDs
			got, err := c.GetXRDs(tc.args.ctx)

			// Handle error check and verification of first call results
			if tc.want.err != nil {
				if err == nil {
					t.Errorf("\n%s\nGetXRDs(...): expected error but got none", tc.reason)
					return
				}

				if diff := cmp.Diff(tc.want.err.Error(), err.Error()); diff != "" {
					t.Errorf("\n%s\nGetXRDs(...): -want error, +got error:\n%s", tc.reason, diff)
				}
				return
			}

			if err != nil {
				t.Errorf("\n%s\nGetXRDs(...): unexpected error: %v", tc.reason, err)
				return
			}

			// For caching test, verify cache state and make a second call if needed
			if tc.secondCall {
				// Verify cache is populated
				if !c.xrdsLoaded {
					t.Errorf("\n%s\nGetXRDs(...): cache not marked as loaded after first call", tc.reason)
				}

				// Make second call to test cache
				gotSecond, errSecond := c.GetXRDs(tc.args.ctx)

				// Verify second call succeeds
				if errSecond != nil {
					t.Errorf("\n%s\nGetXRDs(...): second call unexpected error: %v", tc.reason, errSecond)
					return
				}

				// Verify second call returns same count of XRDs
				if len(got) != len(gotSecond) {
					t.Errorf("\n%s\nGetXRDs(...): second call returned %d XRDs, first call returned %d", tc.reason, len(gotSecond), len(got))
				}

				// Count the actual number of "list" actions in the tracker
				apiCalls := 0
				for _, action := range dc.(*fake.FakeDynamicClient).Fake.Actions() {
					if action.GetVerb() == "list" && action.GetResource().Resource == "compositeresourcedefinitions" {
						apiCalls++
					}
				}

				// Verify API call count
				if tc.apiCallCount > 0 && apiCalls != tc.apiCallCount {
					t.Errorf("\n%s\nGetXRDs(...): expected %d API calls, got %d", tc.reason, tc.apiCallCount, apiCalls)
				}
			}

			// Verify the returned XRD count matches what's expected
			if diff := cmp.Diff(len(tc.want.xrds), len(got)); diff != "" {
				t.Errorf("\n%s\nGetXRDs(...): -want xrd count, +got xrd count:\n%s", tc.reason, diff)
			}

			// Check if we got the right XRDs by name
			// Create maps of XRD names for easier lookup
			wantXRDNames := make(map[string]bool)
			gotXRDNames := make(map[string]bool)

			for _, xrd := range tc.want.xrds {
				wantXRDNames[xrd.GetName()] = true
			}

			for _, xrd := range got {
				gotXRDNames[xrd.GetName()] = true
			}

			for name := range wantXRDNames {
				if !gotXRDNames[name] {
					t.Errorf("\n%s\nGetXRDs(...): missing expected XRD with name %s", tc.reason, name)
				}
			}

			for name := range gotXRDNames {
				if !wantXRDNames[name] {
					t.Errorf("\n%s\nGetXRDs(...): unexpected XRD with name %s", tc.reason, name)
				}
			}
		})
	}
}

func TestClusterClient_GetResource(t *testing.T) {
	scheme := runtime.NewScheme()

	type args struct {
		ctx       context.Context
		gvk       schema.GroupVersionKind
		namespace string
		name      string
	}

	type want struct {
		resource *unstructured.Unstructured
		err      error
	}

	tests := map[string]struct {
		reason string
		setup  func() (dynamic.Interface, discovery.DiscoveryInterface)
		args   args
		want   want
	}{
		"NamespacedResourceFound": {
			reason: "Should return the resource when it exists in a namespace",
			setup: func() (dynamic.Interface, discovery.DiscoveryInterface) {
				// Use the resource builder to create test objects
				objects := []runtime.Object{
					tu.NewResource("example.org/v1", "ExampleResource", "test-resource").
						InNamespace("test-namespace").
						WithSpecField("property", "value").
						Build(),
				}

				dynamicClient := fake.NewSimpleDynamicClient(scheme, objects...)

				// Create fake discovery client with resources
				resources := map[string][]metav1.APIResource{
					"example.org/v1": {
						{
							Name: "exampleresources",
							Kind: "ExampleResource",
						},
					},
				}
				return dynamicClient, createFakeDiscoveryClient(resources)
			},
			args: args{
				ctx: context.Background(),
				gvk: schema.GroupVersionKind{
					Group:   "example.org",
					Version: "v1",
					Kind:    "ExampleResource",
				},
				namespace: "test-namespace",
				name:      "test-resource",
			},
			want: want{
				resource: tu.NewResource("example.org/v1", "ExampleResource", "test-resource").
					InNamespace("test-namespace").
					WithSpecField("property", "value").
					Build(),
			},
		},
		"ClusterScopedResourceFound": {
			reason: "Should return the resource when it exists at cluster scope",
			setup: func() (dynamic.Interface, discovery.DiscoveryInterface) {
				objects := []runtime.Object{
					tu.NewResource("example.org/v1", "ClusterResource", "test-cluster-resource").
						WithSpecField("property", "value").
						Build(),
				}

				dynamicClient := fake.NewSimpleDynamicClient(scheme, objects...)

				// Create fake discovery client with resources
				resources := map[string][]metav1.APIResource{
					"example.org/v1": {
						{
							Name: "clusterresources",
							Kind: "ClusterResource",
						},
					},
				}
				return dynamicClient, createFakeDiscoveryClient(resources)
			},
			args: args{
				ctx: context.Background(),
				gvk: schema.GroupVersionKind{
					Group:   "example.org",
					Version: "v1",
					Kind:    "ClusterResource",
				},
				namespace: "", // Cluster-scoped
				name:      "test-cluster-resource",
			},
			want: want{
				resource: tu.NewResource("example.org/v1", "ClusterResource", "test-cluster-resource").
					WithSpecField("property", "value").
					Build(),
			},
		},
		"ResourceNotFound": {
			reason: "Should return an error when the resource doesn't exist",
			setup: func() (dynamic.Interface, discovery.DiscoveryInterface) {
				dc := fake.NewSimpleDynamicClient(scheme)
				dc.Fake.PrependReactor("get", "*", func(action kt.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("resource not found")
				})

				// Create fake discovery client with resources
				resources := map[string][]metav1.APIResource{
					"example.org/v1": {
						{
							Name: "exampleresources",
							Kind: "ExampleResource",
						},
					},
				}
				return dc, createFakeDiscoveryClient(resources)
			},
			args: args{
				ctx: context.Background(),
				gvk: schema.GroupVersionKind{
					Group:   "example.org",
					Version: "v1",
					Kind:    "ExampleResource",
				},
				namespace: "test-namespace",
				name:      "nonexistent-resource",
			},
			want: want{
				resource: nil,
				err:      errors.New("cannot get resource test-namespace/nonexistent-resource of kind ExampleResource"),
			},
		},
		"SpecialResourceType": {
			reason: "Should handle special resource types with non-standard pluralization",
			setup: func() (dynamic.Interface, discovery.DiscoveryInterface) {
				objects := []runtime.Object{
					tu.NewResource("v1", "Endpoints", "test-endpoints").
						InNamespace("test-namespace").
						WithSpecField("subsets", []interface{}{
							map[string]interface{}{
								"addresses": []interface{}{
									map[string]interface{}{
										"ip": "192.168.1.1",
									},
								},
							},
						}).
						Build(),
				}

				dynamicClient := fake.NewSimpleDynamicClient(scheme, objects...)

				// Create fake discovery client with resources
				resources := map[string][]metav1.APIResource{
					"v1": {
						{
							Name: "endpoints",
							Kind: "Endpoints",
						},
					},
				}
				return dynamicClient, createFakeDiscoveryClient(resources)
			},
			args: args{
				ctx: context.Background(),
				gvk: schema.GroupVersionKind{
					Group:   "",
					Version: "v1",
					Kind:    "Endpoints",
				},
				namespace: "test-namespace",
				name:      "test-endpoints",
			},
			want: want{
				resource: tu.NewResource("v1", "Endpoints", "test-endpoints").
					InNamespace("test-namespace").
					WithSpecField("subsets", []interface{}{
						map[string]interface{}{
							"addresses": []interface{}{
								map[string]interface{}{
									"ip": "192.168.1.1",
								},
							},
						},
					}).
					Build(),
			},
		},
		"DiscoveryError": {
			reason: "Should propagate errors from the discovery client",
			setup: func() (dynamic.Interface, discovery.DiscoveryInterface) {
				dynamicClient := fake.NewSimpleDynamicClient(scheme)

				// Create fake discovery client that returns an error
				fakeDiscovery := &fakediscovery.FakeDiscovery{
					Fake: &kt.Fake{},
				}

				// Set up the discovery client to return an error
				fakeDiscovery.Fake.AddReactor("get", "resources", func(action kt.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("discovery error")
				})

				return dynamicClient, fakeDiscovery
			},
			args: args{
				ctx: context.Background(),
				gvk: schema.GroupVersionKind{
					Group:   "example.org",
					Version: "v1",
					Kind:    "ExampleResource",
				},
				namespace: "test-namespace",
				name:      "test-resource",
			},
			want: want{
				resource: nil,
				err:      errors.New("failed to discover resources for example.org/v1"),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dynamicClient, discoveryClient := tc.setup()

			c := &DefaultClusterClient{
				dynamicClient:   dynamicClient,
				discoveryClient: discoveryClient,
				logger:          tu.TestLogger(t),
				gvkToGVRMap:     make(map[schema.GroupVersionKind]schema.GroupVersionResource),
			}

			got, err := c.GetResource(tc.args.ctx, tc.args.gvk, tc.args.namespace, tc.args.name)

			if tc.want.err != nil {
				if err == nil {
					t.Errorf("\n%s\nGetResource(...): expected error but got none", tc.reason)
					return
				}

				if !strings.Contains(err.Error(), tc.want.err.Error()) {
					t.Errorf("\n%s\nGetResource(...): expected error containing %q, got %q",
						tc.reason, tc.want.err.Error(), err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("\n%s\nGetResource(...): unexpected error: %v", tc.reason, err)
				return
			}

			// Remove resourceVersion from comparison since it's added by the fake client
			gotCopy := got.DeepCopy()
			if gotCopy != nil && gotCopy.Object != nil {
				meta, found, _ := unstructured.NestedMap(gotCopy.Object, "metadata")
				if found && meta != nil {
					delete(meta, "resourceVersion")
					_ = unstructured.SetNestedMap(gotCopy.Object, meta, "metadata")
				}
			}

			if diff := cmp.Diff(tc.want.resource, gotCopy); diff != "" {
				t.Errorf("\n%s\nGetResource(...): -want, +got:\n%s", tc.reason, diff)
			}
		})
	}
}

func TestClusterClient_DryRunApply(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pkgv1.AddToScheme(scheme)
	_ = apiextensionsv1.AddToScheme(scheme)

	type args struct {
		ctx context.Context
		obj *unstructured.Unstructured
	}

	type want struct {
		result *unstructured.Unstructured
		err    error
	}

	tests := map[string]struct {
		reason string
		setup  func() *tu.MockClusterClient
		args   args
		want   want
	}{
		"NamespacedResourceApplied": {
			reason: "Should successfully apply a namespaced resource",
			setup: func() *tu.MockClusterClient {
				return &tu.MockClusterClient{
					DryRunApplyFn: func(ctx context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
						// Create a modified copy of the input object
						result := obj.DeepCopy()
						result.SetResourceVersion("1000")
						return result, nil
					},
				}
			},
			args: args{
				ctx: context.Background(),
				obj: tu.NewResource("example.org/v1", "ExampleResource", "test-resource").
					InNamespace("test-namespace").
					WithSpecField("property", "new-value").
					Build(),
			},
			want: want{
				result: tu.NewResource("example.org/v1", "ExampleResource", "test-resource").
					InNamespace("test-namespace").
					WithSpecField("property", "new-value").
					Build(),
			},
		},
		"ClusterScopedResourceApplied": {
			reason: "Should successfully apply a cluster-scoped resource",
			setup: func() *tu.MockClusterClient {
				return &tu.MockClusterClient{
					DryRunApplyFn: func(ctx context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
						// Create a modified copy of the input object
						result := obj.DeepCopy()
						result.SetResourceVersion("1000")
						return result, nil
					},
				}
			},
			args: args{
				ctx: context.Background(),
				obj: tu.NewResource("example.org/v1", "ClusterResource", "test-cluster-resource").
					WithSpecField("property", "new-value").
					Build(),
			},
			want: want{
				result: tu.NewResource("example.org/v1", "ClusterResource", "test-cluster-resource").
					WithSpecField("property", "new-value").
					Build(),
			},
		},
		"ApplyError": {
			reason: "Should return error when apply fails",
			setup: func() *tu.MockClusterClient {
				return &tu.MockClusterClient{
					DryRunApplyFn: func(ctx context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
						return nil, errors.New("apply failed")
					},
				}
			},
			args: args{
				ctx: context.Background(),
				obj: tu.NewResource("example.org/v1", "ExampleResource", "test-resource").
					InNamespace("test-namespace").
					Build(),
			},
			want: want{
				result: nil,
				err:    errors.New("apply failed"),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Create the mock client using the setup function
			c := tc.setup()

			got, err := c.DryRunApply(tc.args.ctx, tc.args.obj)

			if tc.want.err != nil {
				if err == nil {
					t.Errorf("\n%s\nDryRunApply(...): expected error but got none", tc.reason)
					return
				}

				if !strings.Contains(err.Error(), tc.want.err.Error()) {
					t.Errorf("\n%s\nDryRunApply(...): expected error containing %q, got %q", tc.reason, tc.want.err.Error(), err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("\n%s\nDryRunApply(...): unexpected error: %v", tc.reason, err)
				return
			}

			// For successful cases, compare the original parts of results
			// We remove the resourceVersion before comparing since we set it in our test
			gotCopy := got.DeepCopy()
			if _, exists, _ := unstructured.NestedString(gotCopy.Object, "metadata", "resourceVersion"); exists {
				unstructured.RemoveNestedField(gotCopy.Object, "metadata", "resourceVersion")
			}

			wantCopy := tc.want.result.DeepCopy()
			if _, exists, _ := unstructured.NestedString(wantCopy.Object, "metadata", "resourceVersion"); exists {
				unstructured.RemoveNestedField(wantCopy.Object, "metadata", "resourceVersion")
			}

			if diff := cmp.Diff(wantCopy, gotCopy); diff != "" {
				t.Errorf("\n%s\nDryRunApply(...): -want, +got:\n%s", tc.reason, diff)
			}
		})
	}
}

func TestClusterClient_GetResourcesByLabel(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pkgv1.AddToScheme(scheme)
	_ = apiextensionsv1.AddToScheme(scheme)

	tests := map[string]struct {
		reason string
		setup  func() (dynamic.Interface, discovery.DiscoveryInterface)
		args   struct {
			ctx       context.Context
			namespace string
			gvk       schema.GroupVersionKind
			selector  metav1.LabelSelector
		}
		want struct {
			resources []*unstructured.Unstructured
			err       error
		}
	}{
		"NoMatchingResources": {
			reason: "Should return empty list when no resources match selector",
			setup: func() (dynamic.Interface, discovery.DiscoveryInterface) {
				dc := fake.NewSimpleDynamicClientWithCustomListKinds(scheme,
					map[schema.GroupVersionResource]string{
						{Group: "example.org", Version: "v1", Resource: "resources"}: "ResourceList",
					})

				// Create fake discovery client with resources
				resources := map[string][]metav1.APIResource{
					"example.org/v1": {
						{
							Name: "resources",
							Kind: "Resource",
						},
					},
				}
				return dc, createFakeDiscoveryClient(resources)
			},
			args: struct {
				ctx       context.Context
				namespace string
				gvk       schema.GroupVersionKind
				selector  metav1.LabelSelector
			}{
				ctx:       context.Background(),
				namespace: "test-namespace",
				gvk: schema.GroupVersionKind{
					Group:   "example.org",
					Version: "v1",
					Kind:    "Resource",
				},
				selector: metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "test"},
				},
			},
			want: struct {
				resources []*unstructured.Unstructured
				err       error
			}{
				resources: []*unstructured.Unstructured{},
			},
		},
		"MatchingResources": {
			reason: "Should return resources matching label selector",
			setup: func() (dynamic.Interface, discovery.DiscoveryInterface) {
				// Use resource builders for cleaner test objects
				objects := []runtime.Object{
					// Resource that matches our selector
					tu.NewResource("example.org/v1", "Resource", "matched-resource-1").
						InNamespace("test-namespace").
						WithLabels(map[string]string{
							"app": "test",
							"env": "dev",
						}).
						Build(),

					// Resource that matches our selector with different labels
					tu.NewResource("example.org/v1", "Resource", "matched-resource-2").
						InNamespace("test-namespace").
						WithLabels(map[string]string{
							"app": "test",
							"env": "prod",
						}).
						Build(),

					// Resource that doesn't match our selector
					tu.NewResource("example.org/v1", "Resource", "unmatched-resource").
						InNamespace("test-namespace").
						WithLabels(map[string]string{
							"app": "other",
						}).
						Build(),
				}

				dc := fake.NewSimpleDynamicClient(scheme, objects...)

				// Create fake discovery client with resources
				resources := map[string][]metav1.APIResource{
					"example.org/v1": {
						{
							Name: "resources",
							Kind: "Resource",
						},
					},
				}
				return dc, createFakeDiscoveryClient(resources)
			},
			args: struct {
				ctx       context.Context
				namespace string
				gvk       schema.GroupVersionKind
				selector  metav1.LabelSelector
			}{
				ctx:       context.Background(),
				namespace: "test-namespace",
				gvk: schema.GroupVersionKind{
					Group:   "example.org",
					Version: "v1",
					Kind:    "Resource",
				},
				selector: metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "test"},
				},
			},
			want: struct {
				resources []*unstructured.Unstructured
				err       error
			}{
				resources: []*unstructured.Unstructured{
					// Expected matching resources using builders
					tu.NewResource("example.org/v1", "Resource", "matched-resource-1").
						InNamespace("test-namespace").
						WithLabels(map[string]string{
							"app": "test",
							"env": "dev",
						}).
						Build(),
					tu.NewResource("example.org/v1", "Resource", "matched-resource-2").
						InNamespace("test-namespace").
						WithLabels(map[string]string{
							"app": "test",
							"env": "prod",
						}).
						Build(),
				},
			},
		},
		"ListError": {
			reason: "Should propagate errors from the Kubernetes API",
			setup: func() (dynamic.Interface, discovery.DiscoveryInterface) {
				dc := fake.NewSimpleDynamicClientWithCustomListKinds(scheme,
					map[schema.GroupVersionResource]string{
						{Group: "example.org", Version: "v1", Resource: "resources"}: "ResourceList",
					})

				dc.Fake.PrependReactor("list", "resources", func(action kt.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("list error")
				})

				// Create fake discovery client with resources
				resources := map[string][]metav1.APIResource{
					"example.org/v1": {
						{
							Name: "resources",
							Kind: "Resource",
						},
					},
				}
				return dc, createFakeDiscoveryClient(resources)
			},
			args: struct {
				ctx       context.Context
				namespace string
				gvk       schema.GroupVersionKind
				selector  metav1.LabelSelector
			}{
				ctx:       context.Background(),
				namespace: "test-namespace",
				gvk: schema.GroupVersionKind{
					Group:   "example.org",
					Version: "v1",
					Kind:    "Resource",
				},
				selector: metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "test"},
				},
			},
			want: struct {
				resources []*unstructured.Unstructured
				err       error
			}{
				err: errors.New("cannot list resources for 'example.org/v1, Kind=Resource' matching 'app=test': list error"),
			},
		},
		"DiscoveryError": {
			reason: "Should propagate errors from the discovery client",
			setup: func() (dynamic.Interface, discovery.DiscoveryInterface) {
				dc := fake.NewSimpleDynamicClient(scheme)

				// Create fake discovery client that returns an error
				fakeDiscovery := &fakediscovery.FakeDiscovery{
					Fake: &kt.Fake{},
				}

				// Set up the discovery client to return an error
				fakeDiscovery.Fake.AddReactor("get", "resource", func(action kt.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("discovery error")
				})

				return dc, fakeDiscovery
			},
			args: struct {
				ctx       context.Context
				namespace string
				gvk       schema.GroupVersionKind
				selector  metav1.LabelSelector
			}{
				ctx:       context.Background(),
				namespace: "test-namespace",
				gvk: schema.GroupVersionKind{
					Group:   "example.org",
					Version: "v1",
					Kind:    "Resource",
				},
				selector: metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "test"},
				},
			},
			want: struct {
				resources []*unstructured.Unstructured
				err       error
			}{
				err: errors.New("failed to discover resources for example.org/v1"),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dynamicClient, discoveryClient := tc.setup()

			c := &DefaultClusterClient{
				dynamicClient:   dynamicClient,
				discoveryClient: discoveryClient,
				logger:          tu.TestLogger(t),
				gvkToGVRMap:     make(map[schema.GroupVersionKind]schema.GroupVersionResource),
			}

			got, err := c.GetResourcesByLabel(tc.args.ctx, tc.args.namespace, tc.args.gvk, tc.args.selector)

			if tc.want.err != nil {
				if err == nil {
					t.Errorf("\n%s\nGetResourcesByLabel(...): expected error but got none", tc.reason)
					return
				}

				// Check that the error contains the expected message
				if !strings.Contains(err.Error(), tc.want.err.Error()) {
					t.Errorf("\n%s\nGetResourcesByLabel(...): expected error containing %q, got: %v",
						tc.reason, tc.want.err.Error(), err)
				}
				return
			}

			if err != nil {
				t.Errorf("\n%s\nGetResourcesByLabel(...): unexpected error: %v", tc.reason, err)
				return
			}

			if diff := cmp.Diff(len(tc.want.resources), len(got)); diff != "" {
				t.Errorf("\n%s\nGetResourcesByLabel(...): -want resource count, +got resource count:\n%s", tc.reason, diff)
			}

			// Compare resources by name to handle ordering differences
			wantResources := make(map[string]bool)
			for _, res := range tc.want.resources {
				wantResources[res.GetName()] = true
			}

			for _, gotRes := range got {
				if !wantResources[gotRes.GetName()] {
					t.Errorf("\n%s\nGetResourcesByLabel(...): unexpected resource: %s", tc.reason, gotRes.GetName())
				}
			}

			// Also check if any expected resources are missing
			gotResources := make(map[string]bool)
			for _, res := range got {
				gotResources[res.GetName()] = true
			}

			for _, wantRes := range tc.want.resources {
				if !gotResources[wantRes.GetName()] {
					t.Errorf("\n%s\nGetResourcesByLabel(...): missing expected resource: %s", tc.reason, wantRes.GetName())
				}
			}
		})
	}
}

func TestClusterClient_GetResourceTree(t *testing.T) {
	// Setup test context
	ctx := context.Background()

	// Create test XR
	xr := tu.NewResource("example.org/v1", "XExampleResource", "test-xr").
		WithSpecField("coolParam", "cool-value").
		Build()

	// Create composed resources that would be children of the XR
	composedResource1 := tu.NewResource("composed.org/v1", "ComposedResource", "child-1").
		WithCompositeOwner("test-xr").
		WithCompositionResourceName("resource-1").
		WithSpecField("param", "value-1").
		Build()

	composedResource2 := tu.NewResource("composed.org/v1", "ComposedResource", "child-2").
		WithCompositeOwner("test-xr").
		WithCompositionResourceName("resource-2").
		WithSpecField("param", "value-2").
		Build()

	// Create a test resource tree
	testResourceTree := &resource.Resource{
		Unstructured: *xr.DeepCopy(),
		Children: []*resource.Resource{
			{
				Unstructured: *composedResource1.DeepCopy(),
				Children:     []*resource.Resource{},
			},
			{
				Unstructured: *composedResource2.DeepCopy(),
				Children:     []*resource.Resource{},
			},
		},
	}

	tests := map[string]struct {
		clientSetup  func() *tu.MockClusterClient
		input        *unstructured.Unstructured
		expectOutput *resource.Resource
		expectError  bool
		errorPattern string
	}{
		"SuccessfulResourceTreeFetch": {
			clientSetup: func() *tu.MockClusterClient {
				return tu.NewMockClusterClient().
					WithGetResourceTree(func(ctx context.Context, root *unstructured.Unstructured) (*resource.Resource, error) {
						// Verify the input is our XR
						if root.GetName() != "test-xr" || root.GetKind() != "XExampleResource" {
							return nil, errors.New("unexpected input resource")
						}
						return testResourceTree, nil
					}).
					Build()
			},
			input:        xr,
			expectOutput: testResourceTree,
			expectError:  false,
		},
		"ResourceTreeNotFound": {
			clientSetup: func() *tu.MockClusterClient {
				return tu.NewMockClusterClient().
					WithGetResourceTree(func(ctx context.Context, root *unstructured.Unstructured) (*resource.Resource, error) {
						return nil, errors.New("resource tree not found")
					}).
					Build()
			},
			input:        xr,
			expectOutput: nil,
			expectError:  true,
			errorPattern: "resource tree not found",
		},
		"EmptyResourceTree": {
			clientSetup: func() *tu.MockClusterClient {
				return tu.NewMockClusterClient().
					WithGetResourceTree(func(ctx context.Context, root *unstructured.Unstructured) (*resource.Resource, error) {
						// Return an empty resource tree (just the root, no children)
						return &resource.Resource{
							Unstructured: *root.DeepCopy(),
							Children:     []*resource.Resource{},
						}, nil
					}).
					Build()
			},
			input: xr,
			expectOutput: &resource.Resource{
				Unstructured: *xr.DeepCopy(),
				Children:     []*resource.Resource{},
			},
			expectError: false,
		},
		"NilInputResource": {
			clientSetup: func() *tu.MockClusterClient {
				return tu.NewMockClusterClient().
					WithGetResourceTree(func(ctx context.Context, root *unstructured.Unstructured) (*resource.Resource, error) {
						if root == nil {
							return nil, errors.New("nil resource provided")
						}
						return nil, nil
					}).
					Build()
			},
			input:        nil,
			expectOutput: nil,
			expectError:  true,
			errorPattern: "nil resource provided",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Create the client with our mock implementation
			client := tc.clientSetup()

			// Call the method we're testing
			got, err := client.GetResourceTree(ctx, tc.input)

			// Check for expected errors
			if tc.expectError {
				if err == nil {
					t.Errorf("GetResourceTree() expected error but got none")
					return
				}
				if tc.errorPattern != "" && !strings.Contains(err.Error(), tc.errorPattern) {
					t.Errorf("GetResourceTree() expected error containing %q, got: %v", tc.errorPattern, err)
				}
				return
			}

			// Check for unexpected errors
			if err != nil {
				t.Errorf("GetResourceTree() unexpected error: %v", err)
				return
			}

			// Verify the output matches expectations
			if diff := cmp.Diff(tc.expectOutput, got); diff != "" {
				t.Errorf("GetResourceTree() -want, +got:\n%s", diff)
			}

			// Verify that the tree structure is correct when expected
			if got != nil && tc.expectOutput != nil {
				// Verify root properties
				if diff := cmp.Diff(tc.expectOutput.Unstructured.GetName(), got.Unstructured.GetName()); diff != "" {
					t.Errorf("GetResourceTree() root resource name mismatch -want, +got:\n%s", diff)
				}

				// Verify child count
				if diff := cmp.Diff(len(tc.expectOutput.Children), len(got.Children)); diff != "" {
					t.Errorf("GetResourceTree() child count mismatch -want, +got:\n%s", diff)
				}

				// Verify children names if there are any
				if len(got.Children) > 0 {
					// Create maps of child names for easier comparison
					expectedNames := make(map[string]bool)
					actualNames := make(map[string]bool)

					for _, child := range tc.expectOutput.Children {
						expectedNames[child.Unstructured.GetName()] = true
					}

					for _, child := range got.Children {
						actualNames[child.Unstructured.GetName()] = true
					}

					// Check if any expected children are missing
					for name := range expectedNames {
						if !actualNames[name] {
							t.Errorf("GetResourceTree() missing expected child with name %s", name)
						}
					}

					// Check if there are any unexpected children
					for name := range actualNames {
						if !expectedNames[name] {
							t.Errorf("GetResourceTree() unexpected child with name %s", name)
						}
					}
				}
			}
		})
	}
}

func TestClusterClient_IsCRDRequired(t *testing.T) {
	// Set up context for tests
	ctx := context.Background()

	tests := map[string]struct {
		setupDiscovery func() discovery.DiscoveryInterface
		gvk            schema.GroupVersionKind
		want           bool
	}{
		"CoreResource": {
			setupDiscovery: func() discovery.DiscoveryInterface {
				// Create a fake discovery client that returns core API resources
				fakeDiscovery := &fakediscovery.FakeDiscovery{
					Fake: &kt.Fake{},
				}
				fakeDiscovery.Resources = []*metav1.APIResourceList{
					{
						GroupVersion: "v1",
						APIResources: []metav1.APIResource{
							{
								Name: "pods",
								Kind: "Pod",
							},
						},
					},
				}
				return fakeDiscovery
			},
			gvk: schema.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "Pod",
			},
			want: false, // Core API resource should not require a CRD
		},
		"KubernetesExtensionResource": {
			setupDiscovery: func() discovery.DiscoveryInterface {
				// Create a fake discovery client that returns Kubernetes extension resources
				fakeDiscovery := &fakediscovery.FakeDiscovery{
					Fake: &kt.Fake{},
				}
				fakeDiscovery.Resources = []*metav1.APIResourceList{
					{
						GroupVersion: "apps/v1",
						APIResources: []metav1.APIResource{
							{
								Name: "deployments",
								Kind: "Deployment",
							},
						},
					},
				}
				return fakeDiscovery
			},
			gvk: schema.GroupVersionKind{
				Group:   "apps",
				Version: "v1",
				Kind:    "Deployment",
			},
			want: false, // Kubernetes extension should not require a CRD
		},
		"CustomResource": {
			setupDiscovery: func() discovery.DiscoveryInterface {
				// Create a fake discovery client with no knowledge of this resource
				fakeDiscovery := &fakediscovery.FakeDiscovery{
					Fake: &kt.Fake{},
				}
				fakeDiscovery.Resources = []*metav1.APIResourceList{
					{
						GroupVersion: "v1",
						APIResources: []metav1.APIResource{
							{
								Name: "pods",
								Kind: "Pod",
							},
						},
					},
				}
				return fakeDiscovery
			},
			gvk: schema.GroupVersionKind{
				Group:   "example.org",
				Version: "v1",
				Kind:    "XResource",
			},
			want: true, // Custom resource should require a CRD
		},
		"CustomResourceDiscovered": {
			setupDiscovery: func() discovery.DiscoveryInterface {
				// Create a fake discovery client that is aware of this custom resource
				fakeDiscovery := &fakediscovery.FakeDiscovery{
					Fake: &kt.Fake{},
				}
				fakeDiscovery.Resources = []*metav1.APIResourceList{
					{
						GroupVersion: "example.org/v1",
						APIResources: []metav1.APIResource{
							{
								Name: "xresources",
								Kind: "XResource",
							},
						},
					},
				}
				return fakeDiscovery
			},
			gvk: schema.GroupVersionKind{
				Group:   "example.org",
				Version: "v1",
				Kind:    "XResource",
			},
			want: true, // Custom resource should require a CRD even when discovered
		},
		"APIExtensionResource": {
			setupDiscovery: func() discovery.DiscoveryInterface {
				// Create a fake discovery client that returns apiextensions resources
				fakeDiscovery := &fakediscovery.FakeDiscovery{
					Fake: &kt.Fake{},
				}
				fakeDiscovery.Resources = []*metav1.APIResourceList{
					{
						GroupVersion: "apiextensions.k8s.io/v1",
						APIResources: []metav1.APIResource{
							{
								Name: "customresourcedefinitions",
								Kind: "CustomResourceDefinition",
							},
						},
					},
				}
				return fakeDiscovery
			},
			gvk: schema.GroupVersionKind{
				Group:   "apiextensions.k8s.io",
				Version: "v1",
				Kind:    "CustomResourceDefinition",
			},
			want: true, // APIExtensions resources are handled specially and require CRDs
		},
		"DiscoveryFailure": {
			setupDiscovery: func() discovery.DiscoveryInterface {
				// Create a fake discovery client that returns an error
				fakeDiscovery := &fakediscovery.FakeDiscovery{
					Fake: &kt.Fake{},
				}
				// Set up to generate an error when called
				fakeDiscovery.Fake.AddReactor("*", "*", func(action kt.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("discovery failed")
				})
				return fakeDiscovery
			},
			gvk: schema.GroupVersionKind{
				Group:   "example.org",
				Version: "v1",
				Kind:    "XResource",
			},
			want: true, // Default to requiring CRD on discovery failure
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			logger := tu.TestLogger(t)

			// Create a cluster client with the test discovery client
			c := &DefaultClusterClient{
				discoveryClient: tt.setupDiscovery(),
				logger:          logger,
				resourceMap:     make(map[schema.GroupVersionKind]bool),
			}

			// Call the method under test
			got := c.IsCRDRequired(ctx, tt.gvk)

			// Verify result
			if got != tt.want {
				t.Errorf("IsCRDRequired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClusterClient_GetCRD(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pkgv1.AddToScheme(scheme)
	_ = apiextensionsv1.AddToScheme(scheme)

	type args struct {
		ctx context.Context
		gvk schema.GroupVersionKind
	}

	type want struct {
		crd *unstructured.Unstructured
		err error
	}

	// Create a test CRD as unstructured
	testCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata": map[string]interface{}{
				"name": "xresources.example.org",
			},
			"spec": map[string]interface{}{
				"group": "example.org",
				"names": map[string]interface{}{
					"kind":     "XResource",
					"plural":   "xresources",
					"singular": "xresource",
				},
				"scope": "Namespaced",
				"versions": []interface{}{
					map[string]interface{}{
						"name":    "v1",
						"served":  true,
						"storage": true,
					},
				},
			},
		},
	}

	tests := map[string]struct {
		reason     string
		setup      func() (dynamic.Interface, discovery.DiscoveryInterface)
		args       args
		want       want
		resourceID string
	}{
		"SuccessfulCRDRetrieval": {
			reason: "Should retrieve CRD when it exists",
			setup: func() (dynamic.Interface, discovery.DiscoveryInterface) {
				// Set up the dynamic client to return our test CRD
				dc := fake.NewSimpleDynamicClient(scheme)
				dc.PrependReactor("get", "customresourcedefinitions", func(action kt.Action) (bool, runtime.Object, error) {
					getAction := action.(kt.GetAction)
					if getAction.GetName() == "xresources.example.org" {
						return true, testCRD, nil
					}
					return false, nil, nil
				})

				// Create fake discovery client with resources
				resources := map[string][]metav1.APIResource{
					"example.org/v1": {
						{
							Name: "xresources",
							Kind: "XResource",
						},
					},
				}
				return dc, createFakeDiscoveryClient(resources)
			},
			args: args{
				ctx: context.Background(),
				gvk: schema.GroupVersionKind{
					Group:   "example.org",
					Version: "v1",
					Kind:    "XResource",
				},
			},
			want: want{
				crd: testCRD,
				err: nil,
			},
		},
		"CRDNotFound": {
			reason: "Should return error when CRD doesn't exist",
			setup: func() (dynamic.Interface, discovery.DiscoveryInterface) {
				dc := fake.NewSimpleDynamicClient(scheme)
				dc.PrependReactor("get", "customresourcedefinitions", func(action kt.Action) (bool, runtime.Object, error) {
					return true, nil, apierrors.NewNotFound(
						schema.GroupResource{
							Group:    "apiextensions.k8s.io",
							Resource: "customresourcedefinitions",
						},
						"nonexistentresources.example.org")
				})

				// Create fake discovery client with resources
				resources := map[string][]metav1.APIResource{
					"example.org/v1": {
						{
							Name: "nonexistentresources",
							Kind: "NonexistentResource",
						},
					},
				}
				return dc, createFakeDiscoveryClient(resources)
			},
			args: args{
				ctx: context.Background(),
				gvk: schema.GroupVersionKind{
					Group:   "example.org",
					Version: "v1",
					Kind:    "NonexistentResource",
				},
			},
			want: want{
				crd: nil,
				err: errors.New("cannot get CRD nonexistentresources.example.org for example.org/v1, Kind=NonexistentResource"),
			},
		},
		"ServerError": {
			reason: "Should propagate server errors",
			setup: func() (dynamic.Interface, discovery.DiscoveryInterface) {
				dc := fake.NewSimpleDynamicClient(scheme)
				dc.PrependReactor("get", "customresourcedefinitions", func(action kt.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("server error")
				})

				// Create fake discovery client with resources
				resources := map[string][]metav1.APIResource{
					"example.org/v1": {
						{
							Name: "xresources",
							Kind: "XResource",
						},
					},
				}
				return dc, createFakeDiscoveryClient(resources)
			},
			args: args{
				ctx: context.Background(),
				gvk: schema.GroupVersionKind{
					Group:   "example.org",
					Version: "v1",
					Kind:    "XResource",
				},
			},
			want: want{
				crd: nil,
				err: errors.New("cannot get CRD xresources.example.org for example.org/v1, Kind=XResource"),
			},
		},
		"DiscoveryError": {
			reason: "Should propagate discovery errors",
			setup: func() (dynamic.Interface, discovery.DiscoveryInterface) {
				dc := fake.NewSimpleDynamicClient(scheme)

				// Create fake discovery client that returns an error
				fakeDiscovery := &fakediscovery.FakeDiscovery{
					Fake: &kt.Fake{},
				}

				// Set up to generate an error when called
				fakeDiscovery.Fake.AddReactor("*", "*", func(action kt.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("discovery failed")
				})

				return dc, fakeDiscovery
			},
			args: args{
				ctx: context.Background(),
				gvk: schema.GroupVersionKind{
					Group:   "example.org",
					Version: "v1",
					Kind:    "XResource",
				},
			},
			want: want{
				crd: nil,
				err: errors.New("failed to discover resources for example.org/v1"),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Create the client with discovery setup
			dynamicClient, discoveryClient := tc.setup()

			c := &DefaultClusterClient{
				dynamicClient:   dynamicClient,
				discoveryClient: discoveryClient,
				logger:          tu.TestLogger(t),
				gvkToGVRMap:     make(map[schema.GroupVersionKind]schema.GroupVersionResource),
			}

			// Call the method under test
			crd, err := c.GetCRD(tc.args.ctx, tc.args.gvk)

			// Check for expected errors
			if tc.want.err != nil {
				if err == nil {
					t.Errorf("\n%s\nGetCRD(...): expected error but got none", tc.reason)
					return
				}

				if !strings.Contains(err.Error(), tc.want.err.Error()) {
					t.Errorf("\n%s\nGetCRD(...): expected error to contain %q but got %q",
						tc.reason, tc.want.err.Error(), err.Error())
				}
				return
			}

			// Check for unexpected errors
			if err != nil {
				t.Errorf("\n%s\nGetCRD(...): unexpected error: %v", tc.reason, err)
				return
			}

			// Verify the response matches what we expected
			if crd == nil && tc.want.crd != nil {
				t.Errorf("\n%s\nGetCRD(...): got nil result, want non-nil", tc.reason)
				return
			}

			if crd != nil && tc.want.crd == nil {
				t.Errorf("\n%s\nGetCRD(...): got non-nil result, want nil", tc.reason)
				return
			}

			if crd != nil && tc.want.crd != nil {
				// Compare the name as basic validation
				if crd.GetName() != tc.want.crd.GetName() {
					t.Errorf("\n%s\nGetCRD(...): got CRD named %q, want %q",
						tc.reason, crd.GetName(), tc.want.crd.GetName())
				}
			}
		})
	}
}

// Helper function to create a fake discovery client for testing
func createFakeDiscoveryClient(resources map[string][]metav1.APIResource) discovery.DiscoveryInterface {
	fakeDiscovery := &fakediscovery.FakeDiscovery{
		Fake: &kt.Fake{},
	}

	apiResourceLists := []*metav1.APIResourceList{}

	for gv, apiResources := range resources {
		apiResourceLists = append(apiResourceLists, &metav1.APIResourceList{
			GroupVersion: gv,
			APIResources: apiResources,
		})
	}

	fakeDiscovery.Resources = apiResourceLists
	return fakeDiscovery
}

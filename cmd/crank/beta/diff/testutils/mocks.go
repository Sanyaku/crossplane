package testutils

import (
	"context"
	"github.com/crossplane/crossplane-runtime/pkg/errors"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	apiextensionsv1 "github.com/crossplane/crossplane/apis/apiextensions/v1"
	pkgv1 "github.com/crossplane/crossplane/apis/pkg/v1"
	"github.com/crossplane/crossplane/cmd/crank/beta/internal/resource"
	"io"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
)

// duplicate these interfaces to avoid cyclical dependency:

// DiffProcessor defines the interface for processing resources for diffing
type DiffProcessor interface {
	Initialize(ctx context.Context) error
	ProcessAll(stdout io.Writer, ctx context.Context, resources []*unstructured.Unstructured) error
	ProcessResource(stdout io.Writer, ctx context.Context, res *unstructured.Unstructured) error
}

// ClusterClient defines the interface for interacting with a Kubernetes cluster
type ClusterClient interface {
	Initialize(ctx context.Context) error
	FindMatchingComposition(res *unstructured.Unstructured) (*apiextensionsv1.Composition, error)
	GetEnvironmentConfigs(ctx context.Context) ([]*unstructured.Unstructured, error)
	GetAllResourcesByLabels(ctx context.Context, gvks []schema.GroupVersionKind, selectors []metav1.LabelSelector) ([]*unstructured.Unstructured, error)
	GetFunctionsFromPipeline(comp *apiextensionsv1.Composition) ([]pkgv1.Function, error)
	GetXRDs(ctx context.Context) ([]*unstructured.Unstructured, error)
	GetResource(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error)
	GetResourceTree(ctx context.Context, root *unstructured.Unstructured) (*resource.Resource, error)
	GetResourcesByLabel(ctx context.Context, ns string, gvk schema.GroupVersionKind, sel metav1.LabelSelector) ([]*unstructured.Unstructured, error)
	DryRunApply(ctx context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, error)
	GetCRD(ctx context.Context, gvk schema.GroupVersionKind) (*unstructured.Unstructured, error)
	IsCRDRequired(ctx context.Context, gvk schema.GroupVersionKind) bool
}

// MockDynamicClient mocks the dynamic.Interface
type MockDynamicClient struct {
	ResourceFn func(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface
}

// Resource implements the dynamic.Interface method
func (m *MockDynamicClient) Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return m.ResourceFn(gvr)
}

// MockNamespaceableResourceInterface implements dynamic.NamespaceableResourceInterface
type MockNamespaceableResourceInterface struct {
	NamespaceFn func(namespace string) dynamic.ResourceInterface
	GetFn       func(ctx context.Context, name string, options metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error)
	ListFn      func(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error)
	CreateFn    func(ctx context.Context, obj *unstructured.Unstructured, options metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error)
	UpdateFn    func(ctx context.Context, obj *unstructured.Unstructured, options metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error)
	DeleteFn    func(ctx context.Context, name string, options metav1.DeleteOptions, subresources ...string) error
	PatchFn     func(ctx context.Context, name string, pt types.PatchType, data []byte, options metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error)
}

// Namespace implements dynamic.NamespaceableResourceInterface
func (m *MockNamespaceableResourceInterface) Namespace(namespace string) dynamic.ResourceInterface {
	if m.NamespaceFn != nil {
		return m.NamespaceFn(namespace)
	}
	return &MockResourceInterface{
		GetFn:    m.GetFn,
		ListFn:   m.ListFn,
		CreateFn: m.CreateFn,
		UpdateFn: m.UpdateFn,
		DeleteFn: m.DeleteFn,
		PatchFn:  m.PatchFn,
	}
}

// Create implements dynamic.ResourceInterface
func (m *MockNamespaceableResourceInterface) Create(ctx context.Context, obj *unstructured.Unstructured, options metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, obj, options, subresources...)
	}
	return nil, nil
}

// Update implements dynamic.ResourceInterface
func (m *MockNamespaceableResourceInterface) Update(ctx context.Context, obj *unstructured.Unstructured, options metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, obj, options, subresources...)
	}
	return nil, nil
}

// UpdateStatus implements dynamic.ResourceInterface
func (m *MockNamespaceableResourceInterface) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, options metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return nil, nil
}

// Delete implements dynamic.ResourceInterface
func (m *MockNamespaceableResourceInterface) Delete(ctx context.Context, name string, options metav1.DeleteOptions, subresources ...string) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, name, options, subresources...)
	}
	return nil
}

// DeleteCollection implements dynamic.ResourceInterface
func (m *MockNamespaceableResourceInterface) DeleteCollection(ctx context.Context, options metav1.DeleteOptions, listOptions metav1.ListOptions) error {
	return nil
}

// Get implements dynamic.ResourceInterface
func (m *MockNamespaceableResourceInterface) Get(ctx context.Context, name string, options metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if m.GetFn != nil {
		return m.GetFn(ctx, name, options, subresources...)
	}
	return nil, nil
}

// List implements dynamic.ResourceInterface
func (m *MockNamespaceableResourceInterface) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, opts)
	}
	return nil, nil
}

// Watch implements dynamic.ResourceInterface
func (m *MockNamespaceableResourceInterface) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

// Patch implements dynamic.ResourceInterface
func (m *MockNamespaceableResourceInterface) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, options metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if m.PatchFn != nil {
		return m.PatchFn(ctx, name, pt, data, options, subresources...)
	}
	return nil, nil
}

// Apply implements dynamic.ResourceInterface
func (m *MockNamespaceableResourceInterface) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, options metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return nil, nil
}

// ApplyStatus implements dynamic.ResourceInterface
func (m *MockNamespaceableResourceInterface) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, options metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return nil, nil
}

// MockResourceInterface mocks dynamic.ResourceInterface for namespaced resources
type MockResourceInterface struct {
	GetFn    func(ctx context.Context, name string, options metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error)
	ListFn   func(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error)
	CreateFn func(ctx context.Context, obj *unstructured.Unstructured, options metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error)
	UpdateFn func(ctx context.Context, obj *unstructured.Unstructured, options metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error)
	DeleteFn func(ctx context.Context, name string, options metav1.DeleteOptions, subresources ...string) error
	PatchFn  func(ctx context.Context, name string, pt types.PatchType, data []byte, options metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error)
}

// Create implements dynamic.ResourceInterface
func (m *MockResourceInterface) Create(ctx context.Context, obj *unstructured.Unstructured, options metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, obj, options, subresources...)
	}
	return nil, nil
}

// Update implements dynamic.ResourceInterface
func (m *MockResourceInterface) Update(ctx context.Context, obj *unstructured.Unstructured, options metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, obj, options, subresources...)
	}
	return nil, nil
}

// UpdateStatus implements dynamic.ResourceInterface
func (m *MockResourceInterface) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, options metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return nil, nil
}

// Delete implements dynamic.ResourceInterface
func (m *MockResourceInterface) Delete(ctx context.Context, name string, options metav1.DeleteOptions, subresources ...string) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, name, options, subresources...)
	}
	return nil
}

// DeleteCollection implements dynamic.ResourceInterface
func (m *MockResourceInterface) DeleteCollection(ctx context.Context, options metav1.DeleteOptions, listOptions metav1.ListOptions) error {
	return nil
}

// Get implements dynamic.ResourceInterface
func (m *MockResourceInterface) Get(ctx context.Context, name string, options metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if m.GetFn != nil {
		return m.GetFn(ctx, name, options, subresources...)
	}
	return nil, nil
}

// List implements dynamic.ResourceInterface
func (m *MockResourceInterface) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, opts)
	}
	return nil, nil
}

// Watch implements dynamic.ResourceInterface
func (m *MockResourceInterface) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

// Patch implements dynamic.ResourceInterface
func (m *MockResourceInterface) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, options metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if m.PatchFn != nil {
		return m.PatchFn(ctx, name, pt, data, options, subresources...)
	}
	return nil, nil
}

// Apply implements dynamic.ResourceInterface
func (m *MockResourceInterface) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, options metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return nil, nil
}

// ApplyStatus implements dynamic.ResourceInterface
func (m *MockResourceInterface) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, options metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return nil, nil
}

// MockClusterClient implements the ClusterClient interface for testing
type MockClusterClient struct {
	InitializeFn               func(context.Context) error
	FindMatchingCompositionFn  func(*unstructured.Unstructured) (*apiextensionsv1.Composition, error)
	GetFunctionsFromPipelineFn func(*apiextensionsv1.Composition) ([]pkgv1.Function, error)
	GetXRDsFn                  func(context.Context) ([]*unstructured.Unstructured, error)
	GetResourceFn              func(context.Context, schema.GroupVersionKind, string, string) (*unstructured.Unstructured, error)
	GetResourceTreeFn          func(context.Context, *unstructured.Unstructured) (*resource.Resource, error)
	DryRunApplyFn              func(context.Context, *unstructured.Unstructured) (*unstructured.Unstructured, error)
	GetResourcesByLabelFn      func(context.Context, string, schema.GroupVersionKind, metav1.LabelSelector) ([]*unstructured.Unstructured, error)
	GetEnvironmentConfigsFn    func(context.Context) ([]*unstructured.Unstructured, error)
	GetAllResourcesByLabelsFn  func(context.Context, []schema.GroupVersionKind, []metav1.LabelSelector) ([]*unstructured.Unstructured, error)
	IsCRDRequiredFn            func(ctx context.Context, gvk schema.GroupVersionKind) bool
	GetCRDFn                   func(ctx context.Context, gvk schema.GroupVersionKind) (*unstructured.Unstructured, error)
	logger                     logging.Logger
}

// Initialize implements the ClusterClient interface
func (m *MockClusterClient) Initialize(ctx context.Context) error {
	if m.InitializeFn != nil {
		return m.InitializeFn(ctx)
	}
	return nil
}

// FindMatchingComposition implements the ClusterClient interface
func (m *MockClusterClient) FindMatchingComposition(res *unstructured.Unstructured) (*apiextensionsv1.Composition, error) {
	if m.FindMatchingCompositionFn != nil {
		return m.FindMatchingCompositionFn(res)
	}
	return nil, errors.New("FindMatchingComposition not implemented")
}

// GetAllResourcesByLabels implements the ClusterClient interface
func (m *MockClusterClient) GetAllResourcesByLabels(ctx context.Context, gvks []schema.GroupVersionKind, selectors []metav1.LabelSelector) ([]*unstructured.Unstructured, error) {
	if m.GetAllResourcesByLabelsFn != nil {
		return m.GetAllResourcesByLabelsFn(ctx, gvks, selectors)
	}
	return nil, errors.New("GetAllResourcesByLabels not implemented")
}

// GetFunctionsFromPipeline implements the ClusterClient interface
func (m *MockClusterClient) GetFunctionsFromPipeline(comp *apiextensionsv1.Composition) ([]pkgv1.Function, error) {
	if m.GetFunctionsFromPipelineFn != nil {
		return m.GetFunctionsFromPipelineFn(comp)
	}
	return nil, errors.New("GetFunctionsFromPipeline not implemented")
}

// GetXRDs implements the ClusterClient interface
func (m *MockClusterClient) GetXRDs(ctx context.Context) ([]*unstructured.Unstructured, error) {
	if m.GetXRDsFn != nil {
		return m.GetXRDsFn(ctx)
	}
	return nil, errors.New("GetXRDs not implemented")
}

// GetResource implements the ClusterClient interface
func (m *MockClusterClient) GetResource(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
	if m.GetResourceFn != nil {
		return m.GetResourceFn(ctx, gvk, namespace, name)
	}
	return nil, errors.New("GetResource not implemented")
}

// GetResourceTree implements the ClusterClient interface
func (m *MockClusterClient) GetResourceTree(ctx context.Context, root *unstructured.Unstructured) (*resource.Resource, error) {
	if m.GetResourceTreeFn != nil {
		return m.GetResourceTreeFn(ctx, root)
	}
	return nil, errors.New("GetResourceTree not implemented")
}

// GetResourcesByLabel implements the ClusterClient interface
// Updated to accept GVK instead of GVR
func (m *MockClusterClient) GetResourcesByLabel(ctx context.Context, ns string, gvk schema.GroupVersionKind, selector metav1.LabelSelector) ([]*unstructured.Unstructured, error) {
	if m.GetResourcesByLabelFn != nil {
		return m.GetResourcesByLabelFn(ctx, ns, gvk, selector)
	}
	return nil, errors.New("GetResourcesByLabel not implemented")
}

// DryRunApply implements the ClusterClient interface
func (m *MockClusterClient) DryRunApply(ctx context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if m.DryRunApplyFn != nil {
		return m.DryRunApplyFn(ctx, obj)
	}
	return nil, errors.New("DryRunApply not implemented")
}

// GetEnvironmentConfigs implements the ClusterClient interface
func (m *MockClusterClient) GetEnvironmentConfigs(ctx context.Context) ([]*unstructured.Unstructured, error) {
	if m.GetEnvironmentConfigsFn != nil {
		return m.GetEnvironmentConfigsFn(ctx)
	}
	return nil, errors.New("GetEnvironmentConfigs not implemented")
}

// IsCRDRequired implements the ClusterClient interface
func (m *MockClusterClient) IsCRDRequired(ctx context.Context, gvk schema.GroupVersionKind) bool {
	if m.IsCRDRequiredFn != nil {
		return m.IsCRDRequiredFn(ctx, gvk)
	}
	// Default behavior if not implemented - assume CRD is required
	return true
}

// GetCRD implements the ClusterClient interface
func (m *MockClusterClient) GetCRD(ctx context.Context, gvk schema.GroupVersionKind) (*unstructured.Unstructured, error) {
	if m.GetCRDFn != nil {
		return m.GetCRDFn(ctx, gvk)
	}
	return nil, errors.New("GetCRD not implemented")
}

// MockDiffProcessor implements the DiffProcessor interface for testing
type MockDiffProcessor struct {
	// Function fields for mocking behavior
	InitializeFn  func(ctx context.Context) error
	PerformDiffFn func(stdout io.Writer, ctx context.Context, resources []*unstructured.Unstructured) error
}

// Initialize implements the DiffProcessor interface
func (m *MockDiffProcessor) Initialize(ctx context.Context) error {
	if m.InitializeFn != nil {
		return m.InitializeFn(ctx)
	}
	return nil
}

// PerformDiff implements the DiffProcessor.PerformDiff method
func (m *MockDiffProcessor) PerformDiff(stdout io.Writer, ctx context.Context, resources []*unstructured.Unstructured) error {
	if m.PerformDiffFn != nil {
		return m.PerformDiffFn(stdout, ctx, resources)
	}
	return nil
}

type MockLoader struct {
	Resources []*unstructured.Unstructured
	Err       error
}

func (m *MockLoader) Load() ([]*unstructured.Unstructured, error) {
	return m.Resources, m.Err
}

// Helper type for mocking providers
type MockExtraResourceProvider struct {
	GetExtraResourcesFn func(ctx context.Context, comp *apiextensionsv1.Composition, xr *unstructured.Unstructured, resources []*unstructured.Unstructured) ([]*unstructured.Unstructured, error)
}

func (m *MockExtraResourceProvider) GetExtraResources(ctx context.Context, comp *apiextensionsv1.Composition, xr *unstructured.Unstructured, resources []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	return m.GetExtraResourcesFn(ctx, comp, xr, resources)
}

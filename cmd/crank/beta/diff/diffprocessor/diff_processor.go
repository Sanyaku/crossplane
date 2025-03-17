package diffprocessor

import (
	"context"
	"dario.cat/mergo"
	"fmt"
	"github.com/crossplane/crossplane-runtime/pkg/errors"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	ucomposite "github.com/crossplane/crossplane-runtime/pkg/resource/unstructured/composite"
	apiextensionsv1 "github.com/crossplane/crossplane/apis/apiextensions/v1"
	pkgv1 "github.com/crossplane/crossplane/apis/pkg/v1"
	cc "github.com/crossplane/crossplane/cmd/crank/beta/diff/clusterclient"
	"github.com/crossplane/crossplane/cmd/crank/beta/internal"
	"github.com/crossplane/crossplane/cmd/crank/beta/validate"
	"github.com/crossplane/crossplane/cmd/crank/render"
	"io"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml" // For NewYAMLOrJSONDecoder
	"k8s.io/client-go/rest"
	"strings"
)

// RenderFunc defines the signature of a function that can render resources
type RenderFunc func(ctx context.Context, log logging.Logger, in render.Inputs) (render.Outputs, error)

// DiffProcessor interface for processing resources
type DiffProcessor interface {
	ProcessAll(stdout io.Writer, ctx context.Context, resources []*unstructured.Unstructured) error
	ProcessResource(stdout io.Writer, ctx context.Context, res *unstructured.Unstructured) error
	Initialize(writer io.Writer, ctx context.Context) error
}

// DefaultDiffProcessor handles the processing of resources for diffing.
type DefaultDiffProcessor struct {
	client    cc.ClusterClient
	config    *rest.Config
	namespace string
	renderFn  RenderFunc
	log       logging.Logger
	manager   *validate.Manager
	crds      []*extv1.CustomResourceDefinition
}

// NewDiffProcessor creates a new DefaultDiffProcessor
// If renderFn is nil, it defaults to render.Render
func NewDiffProcessor(config *rest.Config, client cc.ClusterClient, namespace string, renderFn RenderFunc, logger logging.Logger) (DiffProcessor, error) {
	if config == nil {
		return nil, errors.New("config cannot be nil")
	}
	if client == nil {
		return nil, errors.New("client cannot be nil")
	}

	// Default to the standard Render function if none provided
	if renderFn == nil {
		renderFn = render.Render
	}
	if logger == nil {
		logger = logging.NewNopLogger()
	}

	return &DefaultDiffProcessor{
		client:    client,
		config:    config,
		namespace: namespace,
		renderFn:  renderFn,
		log:       logger,
	}, nil
}

func (p *DefaultDiffProcessor) Initialize(writer io.Writer, ctx context.Context) error {
	xrds, err := p.client.GetXRDs(ctx)
	if err != nil {
		return errors.Wrap(err, "cannot get XRDs")
	}

	// Use the helper function to convert XRDs to CRDs
	crds, err := internal.ConvertToCRDs(xrds)
	if err != nil {
		return errors.Wrap(err, "cannot convert XRDs to CRDs")
	}

	// Create a new validation manager
	p.crds = crds

	return nil
}

// ProcessAll handles all resources stored in the processor.
func (p *DefaultDiffProcessor) ProcessAll(stdout io.Writer, ctx context.Context, resources []*unstructured.Unstructured) error {
	var errs []error
	for _, res := range resources {
		if err := p.ProcessResource(stdout, ctx, res); err != nil {
			errs = append(errs, errors.Wrapf(err, "unable to process resource %s", res.GetName()))
		}
	}

	return errors.Join(errs...)
}

// ProcessResource handles one resource at a time.
func (p *DefaultDiffProcessor) ProcessResource(stdout io.Writer, ctx context.Context, res *unstructured.Unstructured) error {
	comp, err := p.client.FindMatchingComposition(res)
	if err != nil {
		return errors.Wrap(err, "cannot find matching composition")
	}

	gvrs, selectors, err := p.IdentifyNeededExtraResources(comp)
	if err != nil {
		return errors.Wrap(err, "cannot identify needed extra resources")
	}

	extraResources, err := p.client.GetAllResourcesByLabels(ctx, gvrs, selectors)
	if err != nil {
		return errors.Wrap(err, "cannot get extra resources")
	}

	fns, err := p.client.GetFunctionsFromPipeline(comp)
	if err != nil {
		return errors.Wrap(err, "cannot get functions from pipeline")
	}

	xr := ucomposite.New()
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(res.UnstructuredContent(), xr); err != nil {
		return errors.Wrap(err, "cannot convert XR to composite unstructured")
	}

	hasTemplatedExtra, err := ScanForTemplatedExtraResources(comp)
	if err != nil {
		return errors.Wrap(err, "cannot scan for templated extra resources")
	}

	if hasTemplatedExtra {
		extraResources, err = p.HandleTemplatedExtraResources(ctx, comp, xr, fns, extraResources)
		if err != nil {
			return err
		}
	}

	desired, err := p.renderFn(ctx, p.log, render.Inputs{
		CompositeResource: xr,
		Composition:       comp,
		Functions:         fns,
		// don't dereference the slice until the last minute
		ExtraResources: internal.DereferenceSlice(extraResources),
	})
	if err != nil {
		return errors.Wrap(err, "cannot render resources")
	}

	// TODO:  comment me in and get me to work
	//if err := p.ValidateResources(stdout, desired); err != nil {
	//	return errors.Wrap(err, "cannot validate resources")
	//}

	printDiff := func(composite string, res runtime.Object) error {
		diff, err := p.CalculateDiff(ctx, composite, res)
		if err != nil {
			return errors.Wrap(err, "cannot calculate diff")
		}
		if diff != "" {
			_, _ = fmt.Fprintf(stdout, "%s\n---\n", diff)
		}

		return nil
	}

	compositeUnstructured := &unstructured.Unstructured{Object: desired.CompositeResource.UnstructuredContent()}

	// the `crossplane render` cli doesn't actually provide the full XR on `render.Outputs`.  it just stuffs
	// the spec from the input XR into the results.  however the input could be different from what's on the server
	// so we should still diff.  so we naively merge the input XR with the rendered XR to get the full XR.
	xrUnstructured, err := mergeUnstructured(compositeUnstructured, res)
	if err != nil {
		return errors.Wrap(err, "cannot merge input XR with result of rendered XR")
	}

	var errs []error
	errs = append(errs, printDiff("", xrUnstructured))

	// Diff the things downstream from the XR
	for _, d := range desired.ComposedResources {
		un := &unstructured.Unstructured{Object: d.UnstructuredContent()}
		errs = append(errs, printDiff(xr.GetName(), un))
	}
	return errors.Wrap(errors.Join(errs...), "cannot print diff")
}

func mergeUnstructured(dest *unstructured.Unstructured, src *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	// Start with a deep copy of the rendered resource
	result := dest.DeepCopy()
	if err := mergo.Merge(&result.Object, src.Object, mergo.WithOverride); err != nil {
		return nil, errors.Wrap(err, "cannot merge unstructured objects")
	}

	return result, nil
}

// IdentifyNeededExtraResources analyzes a composition to determine what extra resources are needed
func (p *DefaultDiffProcessor) IdentifyNeededExtraResources(comp *apiextensionsv1.Composition) ([]schema.GroupVersionResource, []metav1.LabelSelector, error) {
	// If no pipeline mode or no steps, return empty
	if comp.Spec.Mode == nil || *comp.Spec.Mode != apiextensionsv1.CompositionModePipeline {
		return nil, nil, nil
	}

	var resources []schema.GroupVersionResource
	var selectors []metav1.LabelSelector

	// Look for function-extra-resources steps
	for _, step := range comp.Spec.Pipeline {
		if step.FunctionRef.Name != "function-extra-resources" || step.Input == nil {
			continue
		}

		// Parse the input into an unstructured object
		input := &unstructured.Unstructured{}
		if err := input.UnmarshalJSON(step.Input.Raw); err != nil {
			return nil, nil, errors.Wrap(err, "cannot unmarshal function-extra-resources input")
		}

		// Extract extra resources configuration
		extraResources, found, err := unstructured.NestedSlice(input.Object, "spec", "extraResources")
		if err != nil || !found {
			continue
		}

		// Process each extra resource configuration
		for _, er := range extraResources {
			erMap, ok := er.(map[string]interface{})
			if !ok {
				continue
			}

			// Get the resource selector details
			apiVersion, _, _ := unstructured.NestedString(erMap, "apiVersion")
			kind, _, _ := unstructured.NestedString(erMap, "kind")
			selector, _, _ := unstructured.NestedMap(erMap, "selector", "matchLabels")

			if apiVersion == "" || kind == "" {
				continue
			}

			// Create GVR for this resource type
			gv, err := schema.ParseGroupVersion(apiVersion)
			if err != nil {
				return nil, nil, errors.Wrapf(err, "cannot parse group version %q", apiVersion)
			}

			gvr := schema.GroupVersionResource{
				Group:    gv.Group,
				Version:  gv.Version,
				Resource: fmt.Sprintf("%ss", strings.ToLower(kind)), // naive pluralization
			}
			resources = append(resources, gvr)

			// Create label selector
			labelSelector := metav1.LabelSelector{
				MatchLabels: make(map[string]string),
			}
			for k, v := range selector {
				if s, ok := v.(string); ok {
					labelSelector.MatchLabels[k] = s
				}
			}
			selectors = append(selectors, labelSelector)
		}
	}

	return resources, selectors, nil
}

// HandleTemplatedExtraResources processes templated extra resources.
func (p *DefaultDiffProcessor) HandleTemplatedExtraResources(ctx context.Context, comp *apiextensionsv1.Composition, xr *ucomposite.Unstructured, fns []pkgv1.Function, extraResources []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	preliminary, err := render.Render(ctx, nil, render.Inputs{
		CompositeResource: xr,
		Composition:       comp,
		Functions:         fns,
		ExtraResources:    internal.DereferenceSlice(extraResources),
	})
	if err != nil {
		return nil, errors.Wrap(err, "cannot perform preliminary render")
	}

	for _, result := range preliminary.Results {
		if result.GetKind() == "ExtraResources" {
			additional, err := GetExtraResourcesFromResult(&result)
			if err != nil {
				return nil, errors.Wrap(err, "cannot get extra resources from result")
			}
			extraResources = append(extraResources, additional...)
		}
	}

	return extraResources, nil
}

// ValidateResources validates the resources using schema validation
func (p *DefaultDiffProcessor) ValidateResources(writer io.Writer, desired render.Outputs) error {
	// Make sure we have CRDs before validation
	if len(p.crds) == 0 {
		return errors.New("no CRDs available for validation")
	}

	// Convert XR and composed resources to unstructured
	resources := make([]*unstructured.Unstructured, 0, len(desired.ComposedResources)+1)

	// Convert XR from composite.Unstructured to regular Unstructured
	xr := &unstructured.Unstructured{Object: desired.CompositeResource.UnstructuredContent()}
	resources = append(resources, xr)

	// Add composed resources to validation list
	for i := range desired.ComposedResources {
		resources = append(resources, &unstructured.Unstructured{Object: desired.ComposedResources[i].UnstructuredContent()})
	}

	// Validate using the converted CRD schema
	if err := validate.SchemaValidation(resources, p.crds, true, writer); err != nil {
		return errors.Wrap(err, "schema validation failed")
	}

	return nil
}

func ScanForTemplatedExtraResources(comp *apiextensionsv1.Composition) (bool, error) {
	if comp.Spec.Mode == nil || *comp.Spec.Mode != apiextensionsv1.CompositionModePipeline {
		return false, nil
	}

	for _, step := range comp.Spec.Pipeline {
		if step.FunctionRef.Name != "function-go-templating" || step.Input == nil {
			continue
		}

		// Parse the input into an unstructured object
		input := &unstructured.Unstructured{}
		if err := input.UnmarshalJSON(step.Input.Raw); err != nil {
			return false, errors.Wrap(err, "cannot unmarshal function-go-templating input")
		}

		// Look for template string
		template, found, err := unstructured.NestedString(input.Object, "spec", "inline", "template")
		if err != nil || !found {
			continue
		}

		// Parse the template string as YAML and look for ExtraResources documents
		decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(template), 4096)
		for {
			obj := make(map[string]interface{})
			if err := decoder.Decode(&obj); err != nil {
				if err == io.EOF {
					break
				}
				return false, errors.Wrap(err, "cannot decode template YAML")
			}

			u := &unstructured.Unstructured{Object: obj}
			if u.GetKind() == "ExtraResources" {
				return true, nil
			}
		}
	}

	return false, nil
}

func GetExtraResourcesFromResult(result *unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	spec, found, err := unstructured.NestedMap(result.Object, "spec")
	if err != nil || !found {
		return nil, errors.New("no spec found in ExtraResources result")
	}

	extraResources, found, err := unstructured.NestedSlice(spec, "resources")
	if err != nil || !found {
		return nil, errors.New("no resources found in ExtraResources spec")
	}

	var resources []*unstructured.Unstructured
	for _, er := range extraResources {
		erMap, ok := er.(map[string]interface{})
		if !ok {
			continue
		}

		u := unstructured.Unstructured{Object: erMap}
		resources = append(resources, &u)
	}

	return resources, nil
}

// CalculateDiff calculates the difference between desired state and current state
// using the ClusterClient's DryRunApply method and a git-based diff to produce GNU-format diffs
func (p *DefaultDiffProcessor) CalculateDiff(ctx context.Context, composite string, desired runtime.Object) (string, error) {
	// Convert desired to unstructured
	desiredUnstr, ok := desired.(*unstructured.Unstructured)
	if !ok {
		return "", errors.New("desired object is not unstructured")
	}

	// Fetch current object from cluster
	current, _, err := p.fetchCurrentObject(ctx, composite, desiredUnstr)
	if err != nil {
		return "", err
	}

	// TODO:  we need to find all objects, no matter the GVK, that have the composition label
	// so we can diff removals

	// Clean up objects for comparison (remove server-side fields)
	//cleanDesired := cleanupForDiff(desiredUnstr.DeepCopy())

	// For new objects, we don't have a current object to compare against
	//if isNewObject {
	//	current = &unstructured.Unstructured{}
	//	current.SetGroupVersionKind(desiredUnstr.GroupVersionKind())
	//	current.SetName(desiredUnstr.GetName())
	//	current.SetNamespace(desiredUnstr.GetNamespace())

	// TODO:  use the diff calculator to render the new object.

	// Instead of calculating a diff, just format the entire desired object as new
	//desiredYAML, err := sigsyaml.Marshal(cleanDesired.Object)
	//if err != nil {
	//	return "", errors.Wrap(err, "cannot marshal desired object to YAML")
	//}
	//return fmt.Sprintf("+ %s (new object)\n%s", desiredUnstr.GetKind(), string(desiredYAML)), nil
	//}

	// For existing objects, clean up the current object and perform diff
	//cleanCurrent := cleanupForDiff(current.DeepCopy())

	return GenerateDiff(current, desiredUnstr, desiredUnstr.GetKind(), desiredUnstr.GetName())
}

// fetchCurrentObject retrieves the current state of the object from the cluster
// It returns the current object, a boolean indicating if it's a new object, and any error
func (p *DefaultDiffProcessor) fetchCurrentObject(ctx context.Context, composite string, desired *unstructured.Unstructured) (*unstructured.Unstructured, bool, error) {
	// Get the GroupVersionKind and name/namespace for lookup
	gvk := desired.GroupVersionKind()
	name := desired.GetName()
	namespace := desired.GetNamespace()

	var current *unstructured.Unstructured
	var err error
	isNewObject := false

	if composite != "" {
		// For composed resources, use the label selector approach
		sel := metav1.LabelSelector{
			MatchLabels: map[string]string{
				"crossplane.io/composite": composite,
			},
		}
		gvr := schema.GroupVersionResource{
			Group:    gvk.Group,
			Version:  gvk.Version,
			Resource: fmt.Sprintf("%ss", strings.ToLower(gvk.Kind)), // naive pluralization
		}

		// Get the current object from the cluster using ClusterClient
		currents, err := p.client.GetResourcesByLabel(ctx, namespace, gvr, sel)
		if err != nil {
			return nil, false, errors.Wrap(err, "cannot get current object")
		}
		if len(currents) > 1 {
			return nil, false, errors.New(fmt.Sprintf("more than one matching resource found for %s/%s", gvk.Kind, name))
		}

		if len(currents) == 1 {
			current = currents[0]
		} else {
			isNewObject = true
		}
	} else {
		// For XRs, use direct lookup by name
		current, err = p.client.GetResource(ctx, gvk, namespace, name)
		if apierrors.IsNotFound(err) {
			isNewObject = true
		} else if err != nil {
			return nil, false, errors.Wrap(err, "cannot get current object")
		}
	}

	return current, isNewObject, nil
}

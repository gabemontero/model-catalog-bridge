package backstage

// KindResource defines name for resource kind.
const KindResource = "Resource"

// So there are upstream projects which took the backstage schema definitions, such as
// https://github.com/backstage/backstage/blob/master/packages/catalog-model/src/schema/kinds/Resource.v1alpha1.schema.json
// and attempted to auto generate Golang based structs for the Resource entity.
// However, for reasons we want to try and find, the upstream backstage schema files have not been kept up to date
// with the latest format we see when using the UI. The additions we need for the AI Model Catalog format we've devised
// have been made.  And until we can sort out the upstream schema update policy, we'll have to track changes we want
// to pull and make those manually for now.

type ResourceEntityV1alpha1 struct {
	Entity

	// ApiVersion is always "backstage.io/v1alpha1".
	ApiVersion string `json:"apiVersion" yaml:"apiVersion"`

	// Kind is always "Resource".
	Kind string `json:"kind" yaml:"kind"`

	// Spec is the specification data describing the resource itself.
	Spec *ResourceEntityV1alpha1Spec `json:"spec" yaml:"spec"`
}

// ResourceEntityV1alpha1Spec describes the specification data describing the resource itself.
type ResourceEntityV1alpha1Spec struct {
	// Type of resource.
	Type string `json:"type" yaml:"type"`

	//FIX Lifecycle state of the component.
	Lifecycle string `json:"lifecycle" yaml:"lifecycle"`

	// Owner is an entity reference to the owner of the resource.
	Owner string `json:"owner" yaml:"owner"`

	//FIX from schema ProvidesApis is an array of entity references to the APIs that are provided by the component.
	ProvidesApis []string `json:"providesApis,omitempty" yaml:"providesApis,omitempty"`

	//FIX from schema
	DependencyOf []string `json:"dependencyOf,omitempty" yaml:"dependencyOf,omitempty"`

	// System is an entity reference to the system that the resource belongs to.
	System string `json:"system,omitempty" yaml:"system,omitempty"`

	//FIX from schema
	Profile Profile `json:"profile" yaml:"profile"`
}

package config

import (
	"context"
	// Note(turkenh): we are importing this to embed provider schema document
	_ "embed"

	"github.com/crossplane/upjet/v2/pkg/registry/reference"
	"github.com/hashicorp/terraform-provider-dns/xpprovider"

	ujconfig "github.com/crossplane/upjet/v2/pkg/config"

	recordCluster "github.com/dana-team/provider-dns-v2/config/cluster/record"
	recordSetCluster "github.com/dana-team/provider-dns-v2/config/cluster/recordset"
	recordNamespaced "github.com/dana-team/provider-dns-v2/config/namespaced/record"
	recordSetNamespaced "github.com/dana-team/provider-dns-v2/config/namespaced/recordset"
)

const (
	resourcePrefix           = "dns-v2"
	namespacedResourcePrefix = "dns-v2.m"
	modulePath               = "github.com/dana-team/provider-dns-v2"
)

//go:embed schema.json
var providerSchema string

//go:embed provider-metadata.yaml
var providerMetadata string

// GetProvider returns provider configuration
func GetProvider(ctx context.Context) *ujconfig.Provider {
	fwProvider, p := xpprovider.GetProvider(ctx)

	pc := ujconfig.NewProvider([]byte(providerSchema), resourcePrefix, modulePath, []byte(providerMetadata),
		ujconfig.WithRootGroup("dns-v2.crossplane.io"),
		ujconfig.WithShortName("dns-v2"),
		ujconfig.WithIncludeList(resourceList(cliReconciledExternalNameConfigs)),
		ujconfig.WithTerraformPluginSDKIncludeList(resourceList(terraformPluginSDKExternalNameConfigs)),
		ujconfig.WithTerraformPluginFrameworkIncludeList(resourceList(terraformPluginFrameworkExternalNameConfigs)),
		ujconfig.WithDefaultResourceOptions(
			resourceConfigurator(),
		),
		ujconfig.WithReferenceInjectors([]ujconfig.ReferenceInjector{reference.NewInjector(modulePath)}),
		ujconfig.WithFeaturesPackage("internal/features"),
		ujconfig.WithTerraformProvider(p),
		ujconfig.WithTerraformPluginFrameworkProvider(fwProvider),
	)

	for _, configure := range []func(provider *ujconfig.Provider){
		// add custom config functions
		recordCluster.Configure,
		recordSetCluster.Configure,
	} {
		configure(pc)
	}

	pc.ConfigureResources()
	return pc
}

// GetProviderNamespaced returns the namespaced provider configuration
func GetProviderNamespaced(ctx context.Context) *ujconfig.Provider {
	fwProvider, p := xpprovider.GetProvider(ctx)

	pc := ujconfig.NewProvider([]byte(providerSchema), namespacedResourcePrefix, modulePath, []byte(providerMetadata),
		ujconfig.WithRootGroup("dns-v2.m.crossplane.io"),
		ujconfig.WithShortName("dns-v2"),
		ujconfig.WithIncludeList(resourceList(cliReconciledExternalNameConfigs)),
		ujconfig.WithTerraformPluginSDKIncludeList(resourceList(terraformPluginSDKExternalNameConfigs)),
		ujconfig.WithTerraformPluginFrameworkIncludeList(resourceList(terraformPluginFrameworkExternalNameConfigs)),
		ujconfig.WithDefaultResourceOptions(
			resourceConfigurator(),
		),
		ujconfig.WithReferenceInjectors([]ujconfig.ReferenceInjector{reference.NewInjector(modulePath)}),
		ujconfig.WithFeaturesPackage("internal/features"),
		ujconfig.WithTerraformProvider(p),
		ujconfig.WithTerraformPluginFrameworkProvider(fwProvider),
	)

	for _, configure := range []func(provider *ujconfig.Provider){
		recordNamespaced.Configure,
		recordSetNamespaced.Configure,
	} {
		configure(pc)
	}
	pc.ConfigureResources()
	return pc
}

// resourceList returns the list of resources that have external
// name configured in the specified table.
func resourceList(t map[string]ujconfig.ExternalName) []string {
	l := make([]string, len(t))
	i := 0
	for n := range t {
		// Expected format is regex and we'd like to have exact matches.
		l[i] = n + "$"
		i++
	}
	return l
}

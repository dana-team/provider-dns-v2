package clients

import (
	"context"
	"encoding/json"

	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	tfsdk "github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/hashicorp/terraform-provider-dns/xpprovider"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane/upjet/v2/pkg/terraform"

	clusterv1beta1 "github.com/dana-team/provider-dns-v2/apis/cluster/v1beta1"
	namespacedv1beta1 "github.com/dana-team/provider-dns-v2/apis/namespaced/v1beta1"
)

const (
	// error messages
	errNoProviderConfig     = "no providerConfigRef provided"
	errGetProviderConfig    = "cannot get referenced ProviderConfig"
	errTrackUsage           = "cannot track ProviderConfig usage"
	errExtractCredentials   = "cannot extract credentials"
	errUnmarshalCredentials = "cannot unmarshal dns-v2 credentials as JSON"

	// general parameters
	keyRFC       = "rfc"
	keyServer    = "server"
	update       = "update"
	keyPort      = "port"
	keyRetries   = "retries"
	keyTimeout   = "timeout"
	keyTransport = "transport"

	// gss-tsig (RFC 3645) parameters
	gsstsigRFC  = "3645"
	gssapi      = "gssapi"
	keyTab      = "keytab"
	keyPassword = "password"
	keyRealm    = "realm"
	keyUsername = "username"

	// secret key based transaction authentication (RFC 2845) parameters
	keyBasedTransactionRFC  = "2845"
	transactionKeyAlgorithm = "key_algorithm"
	transcationKeyName      = "key_name"
	transactionKeySecret    = "key_secret"
)

// TerraformSetupBuilder builds Terraform a terraform.SetupFn function which
// returns Terraform provider setup configuration
func TerraformSetupBuilder(version, providerSource, providerVersion string, tfProvider *schema.Provider) terraform.SetupFn {
	return func(ctx context.Context, client client.Client, mg resource.Managed) (terraform.Setup, error) {
		ps := terraform.Setup{
			Version: version,
			Requirement: terraform.ProviderRequirement{
				Source:  providerSource,
				Version: providerVersion,
			},
		}

		pcSpec, err := resolveProviderConfig(ctx, client, mg)
		if err != nil {
			return terraform.Setup{}, errors.Wrap(err, "cannot resolve provider config")
		}

		data, err := resource.CommonCredentialExtractor(ctx, pcSpec.Credentials.Source, client, pcSpec.Credentials.CommonCredentialSelectors)
		if err != nil {
			return ps, errors.Wrap(err, errExtractCredentials)
		}
		creds := map[string]string{}
		if err := json.Unmarshal(data, &creds); err != nil {
			return ps, errors.Wrap(err, errUnmarshalCredentials)
		}

		// Set credentials in Terraform provider configuration.
		ps.Configuration = map[string]any{}
		authConfig := buildAuthConfig(creds)
		ps.Configuration[update] = []map[string]any{authConfig}
		return ps, errors.Wrap(configureNoForkDNSClient(ctx, &ps, *tfProvider), "failed to configure the Terraform DNS provider meta")
	}
}

// configureNoForkDNSClient populates the supplied *terraform.Setup with
// Terraform Plugin SDK style DNS client (Meta) and Terraform Plugin Framework
// style FrameworkProvider
func configureNoForkDNSClient(ctx context.Context, ps *terraform.Setup, p schema.Provider) error {
	diag := p.Configure(context.WithoutCancel(ctx), &tfsdk.ResourceConfig{
		Config: ps.Configuration,
	})
	if diag != nil && diag.HasError() {
		return errors.Errorf("failed to configure the provider: %v", diag)
	}
	ps.Meta = p.Meta()
	fwProvider, _ := xpprovider.GetProvider(ctx)
	ps.FrameworkProvider = fwProvider

	return nil
}

func toSharedPCSpec(pc *clusterv1beta1.ProviderConfig) (*namespacedv1beta1.ProviderConfigSpec, error) {
	if pc == nil {
		return nil, nil
	}
	data, err := json.Marshal(pc.Spec)
	if err != nil {
		return nil, err
	}

	var mSpec namespacedv1beta1.ProviderConfigSpec
	err = json.Unmarshal(data, &mSpec)
	return &mSpec, err
}

func resolveProviderConfig(ctx context.Context, crClient client.Client, mg resource.Managed) (*namespacedv1beta1.ProviderConfigSpec, error) {
	switch managed := mg.(type) {
	case resource.LegacyManaged:
		return resolveLegacy(ctx, crClient, managed)
	case resource.ModernManaged:
		return resolveModern(ctx, crClient, managed)
	default:
		return nil, errors.New("resource is not a managed resource")
	}
}

func resolveLegacy(ctx context.Context, client client.Client, mg resource.LegacyManaged) (*namespacedv1beta1.ProviderConfigSpec, error) {
	configRef := mg.GetProviderConfigReference()
	if configRef == nil {
		return nil, errors.New(errNoProviderConfig)
	}
	pc := &clusterv1beta1.ProviderConfig{}
	if err := client.Get(ctx, types.NamespacedName{Name: configRef.Name}, pc); err != nil {
		return nil, errors.Wrap(err, errGetProviderConfig)
	}

	t := resource.NewLegacyProviderConfigUsageTracker(client, &clusterv1beta1.ProviderConfigUsage{})
	if err := t.Track(ctx, mg); err != nil {
		return nil, errors.Wrap(err, errTrackUsage)
	}

	return toSharedPCSpec(pc)
}

func resolveModern(ctx context.Context, crClient client.Client, mg resource.ModernManaged) (*namespacedv1beta1.ProviderConfigSpec, error) {
	configRef := mg.GetProviderConfigReference()
	if configRef == nil {
		return nil, errors.New(errNoProviderConfig)
	}

	pcRuntimeObj, err := crClient.Scheme().New(namespacedv1beta1.SchemeGroupVersion.WithKind(configRef.Kind))
	if err != nil {
		return nil, errors.Wrap(err, "unknown GVK for ProviderConfig")
	}
	pcObj, ok := pcRuntimeObj.(client.Object)
	if !ok {
		// This indicates a programming error, types are not properly generated
		return nil, errors.New(" is not an Object")
	}

	// Namespace will be ignored if the PC is a cluster-scoped type
	if err := crClient.Get(ctx, types.NamespacedName{Name: configRef.Name, Namespace: mg.GetNamespace()}, pcObj); err != nil {
		return nil, errors.Wrap(err, errGetProviderConfig)
	}

	var pcSpec namespacedv1beta1.ProviderConfigSpec
	pcu := &namespacedv1beta1.ProviderConfigUsage{}
	switch pc := pcObj.(type) {
	case *namespacedv1beta1.ProviderConfig:
		pcSpec = pc.Spec
		if pcSpec.Credentials.SecretRef != nil {
			pcSpec.Credentials.SecretRef.Namespace = mg.GetNamespace()
		}
	case *namespacedv1beta1.ClusterProviderConfig:
		pcSpec = pc.Spec
	default:
		return nil, errors.New("unknown provider config type")
	}
	t := resource.NewProviderConfigUsageTracker(crClient, pcu)
	if err := t.Track(ctx, mg); err != nil {
		return nil, errors.Wrap(err, errTrackUsage)
	}
	return &pcSpec, nil
}

// buildAuthConfig builds the auth configuration for the provider.
func buildAuthConfig(creds map[string]string) map[string]any {
	config := map[string]any{}

	if server, ok := creds[keyServer]; ok {
		config[keyServer] = server
	}

	if rfc, ok := creds[keyRFC]; ok {
		switch rfc {
		case gsstsigRFC:
			authConfig := buildGSSTSIGAuthConfig(creds)
			config[gssapi] = []map[string]any{authConfig}
		case keyBasedTransactionRFC:
			secretBasedTransactionAuthConfig := buildSecretBasedTransactionAuthConfig(creds)
			mergeMaps(config, secretBasedTransactionAuthConfig)
		}
	}

	optionalConfig := buildOptionalConfig(creds)
	mergeMaps(config, optionalConfig)

	return config

}

// buildGSSTSIGAuthConfig builds the configuration for GSS-TSIG authentication (RFC 3645).
func buildGSSTSIGAuthConfig(creds map[string]string) map[string]any {
	config := make(map[string]any)

	if realm, ok := creds[keyRealm]; ok {
		config[keyRealm] = realm
	}

	if username, ok := creds[keyUsername]; ok {
		config[keyUsername] = username
	}

	if password, ok := creds[keyPassword]; ok {
		config[keyPassword] = password
	}

	if keytab, ok := creds[keyTab]; ok {
		config[keyTab] = keytab
	}

	return config
}

// // buildGSSTSIGAuthConfig builds the configuration for GSS-TSIG authentication (RFC 2845).
func buildSecretBasedTransactionAuthConfig(creds map[string]string) map[string]any {
	config := make(map[string]any)

	if keyName, ok := creds[transcationKeyName]; ok {
		config[transcationKeyName] = keyName
	}

	if keyAlgorithm, ok := creds[transactionKeyAlgorithm]; ok {
		config[transactionKeyAlgorithm] = keyAlgorithm
	}

	if keySecret, ok := creds[transactionKeySecret]; ok {
		config[transactionKeySecret] = keySecret
	}

	return config
}

// buildOptionalConfig builds the optional configuration for the provider.
func buildOptionalConfig(creds map[string]string) map[string]any {
	config := make(map[string]any)

	if port, ok := creds[keyPort]; ok {
		config[keyPort] = port
	}

	if retries, ok := creds[keyRetries]; ok {
		config[keyRetries] = retries
	}

	if timeout, ok := creds[keyTimeout]; ok {
		config[keyTimeout] = timeout
	}

	if transport, ok := creds[keyTransport]; ok {
		config[keyTransport] = transport
	}

	return config
}

// mergeMaps takes all the keys in B and inserts them into A.
func mergeMaps(a, b map[string]any) {
	for k, v := range b {
		a[k] = v
	}
}

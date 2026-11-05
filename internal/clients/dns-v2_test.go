package clients

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/crossplane/upjet/v2/pkg/terraform"
	tfsdk "github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/hashicorp/terraform-provider-dns/xpprovider"
	"github.com/miekg/dns"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apisCluster "github.com/dana-team/provider-dns-v2/apis/cluster"
	recordsetv1alpha1 "github.com/dana-team/provider-dns-v2/apis/cluster/recordset/v1alpha1"
	clusterv1beta1 "github.com/dana-team/provider-dns-v2/apis/cluster/v1beta1"
)

const (
	testSecretName      = "dns-creds"
	testSecretNamespace = "crossplane-system"
	testSecretKey       = "credentials"
)

// setupFor runs the real SetupFn for a cluster-scoped ARecordSet whose
// ProviderConfig points at a Secret holding the given credentials.
func setupFor(t *testing.T, creds map[string]string) (terraform.Setup, error) {
	t.Helper()

	data, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("cannot marshal credentials: %v", err)
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("cannot add client-go APIs to scheme: %v", err)
	}
	if err := apisCluster.AddToScheme(scheme); err != nil {
		t.Fatalf("cannot add cluster APIs to scheme: %v", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testSecretName, Namespace: testSecretNamespace},
		Data:       map[string][]byte{testSecretKey: data},
	}
	pc := &clusterv1beta1.ProviderConfig{ObjectMeta: metav1.ObjectMeta{Name: "default"}}
	pc.Spec.Credentials.Source = xpv2.CredentialsSourceSecret
	pc.Spec.Credentials.CommonCredentialSelectors = xpv2.CommonCredentialSelectors{
		SecretRef: &xpv2.SecretKeySelector{
			SecretReference: xpv2.SecretReference{Name: testSecretName, Namespace: testSecretNamespace},
			Key:             testSecretKey,
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret, pc).Build()

	mr := &recordsetv1alpha1.ARecordSet{ObjectMeta: metav1.ObjectMeta{Name: "test", UID: types.UID("test-uid")}}
	mr.Spec.ProviderConfigReference = &xpv2.Reference{Name: "default"}
	var mg resource.Managed = mr

	return TerraformSetupBuilder("1.5.7", "hashicorp/dns", "3.6.1")(context.Background(), kube, mg)
}

// startDNSServer starts a local UDP DNS server which answers every question
// with the given record data, and returns the port it is listening on.
func startDNSServer(t *testing.T, rrType, rdata string) string {
	t.Helper()

	var lc net.ListenConfig
	conn, err := lc.ListenPacket(context.Background(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot listen: %v", err)
	}

	srv := &dns.Server{
		PacketConn: conn,
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
			m := new(dns.Msg)
			m.SetReply(r)
			rr, err := dns.NewRR(r.Question[0].Name + " 300 " + rrType + " " + rdata)
			if err != nil {
				t.Errorf("cannot build answer: %v", err)
				return
			}
			m.Answer = []dns.RR{rr}
			if err := w.WriteMsg(m); err != nil {
				t.Errorf("cannot write answer: %v", err)
			}
		}),
	}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })

	return strconv.Itoa(conn.LocalAddr().(*net.UDPAddr).Port)
}

// TestSetupAllowsSDKResourcesToReadRecords reproduces the path upjet uses to
// Observe resources built on the Terraform Plugin SDK. These resources get
// their DNS client from Setup.Meta, unlike Terraform Plugin Framework resources.
func TestSetupAllowsSDKResourcesToReadRecords(t *testing.T) {
	cases := map[string]struct {
		resource string
		rrType   string
		rdata    string
	}{
		"A":    {resource: "dns_a_record_set", rrType: "A", rdata: "10.1.30.1"},
		"AAAA": {resource: "dns_aaaa_record_set", rrType: "AAAA", rdata: "fd00::1"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			port := startDNSServer(t, tc.rrType, tc.rdata)

			ts, err := setupFor(t, map[string]string{keyServer: "127.0.0.1", keyPort: port})
			if err != nil {
				t.Fatalf("setup failed: %v", err)
			}

			ctx := context.Background()
			_, sdkProvider := xpprovider.GetProvider(ctx)
			state := &tfsdk.InstanceState{
				ID:         "www.example.com.",
				Attributes: map[string]string{"id": "www.example.com.", "name": "www", "zone": "example.com."},
			}

			got, diags := sdkProvider.ResourcesMap[tc.resource].RefreshWithoutUpgrade(ctx, state, ts.Meta)
			if diags.HasError() {
				t.Fatalf("read failed: %+v", diags)
			}
			if got == nil || got.ID == "" {
				t.Fatalf("expected the record to be found, got state %+v", got)
			}
			if got.Attributes["addresses.#"] != "1" || got.Attributes["ttl"] != "300" {
				t.Errorf("unexpected attributes read from DNS: %+v", got.Attributes)
			}
		})
	}
}

// TestSetupConfiguresEachAuthModel covers configuration only, since neither
// model contacts the DNS server or KDC until a record is read or written.
func TestSetupConfiguresEachAuthModel(t *testing.T) {
	cases := map[string]map[string]string{
		"RFC3645": {
			keyRFC: gsstsigRFC, keyServer: "dns.example.com",
			keyRealm: "EXAMPLE.COM", keyUsername: "user", keyPassword: "password",
		},
		"RFC2845": {
			keyRFC: keyBasedTransactionRFC, keyServer: "dns.example.com",
			transcationKeyName: "key.example.com.", transactionKeyAlgorithm: "hmac-sha256", transactionKeySecret: "c2VjcmV0",
		},
	}

	for name, creds := range cases {
		t.Run(name, func(t *testing.T) {
			ts, err := setupFor(t, creds)
			if err != nil {
				t.Fatalf("setup failed: %v", err)
			}
			if ts.Meta == nil {
				t.Error("expected Setup.Meta to hold the DNS client")
			}
			if ts.FrameworkProvider == nil {
				t.Error("expected Setup.FrameworkProvider to be set")
			}
		})
	}
}

func TestSetupRejectsInvalidCredentials(t *testing.T) {
	_, err := setupFor(t, map[string]string{
		keyRFC: keyBasedTransactionRFC, keyServer: "dns.example.com",
		transcationKeyName: "key.example.com.",
	})
	if err == nil {
		t.Fatal("expected incomplete RFC 2845 credentials to fail setup")
	}
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	extapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/jetstack/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	"github.com/jetstack/cert-manager/pkg/acme/webhook/cmd"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/dns"
)

var GroupName = os.Getenv("GROUP_NAME")

func main() {
	if GroupName == "" {
		panic("GROUP_NAME must be specified")
	}

	cmd.RunWebhookServer(GroupName,
		&ociDNSProviderSolver{},
	)
}

type ociDNSProviderSolver struct {
	client *kubernetes.Clientset
}

type ociDNSProviderConfig struct {
	CompartmentOCID     string `json:"compartmentOCID"`
	OCIProfileSecretRef string `json:"ociProfileSecretName"`
}

func (c *ociDNSProviderSolver) Name() string {
	return "oci"
}

func (c *ociDNSProviderSolver) Present(ch *v1alpha1.ChallengeRequest) error {
	klog.V(6).Infof("call function Present: namespace=%s, zone=%s, fqdn=%s", ch.ResourceNamespace, ch.ResolvedZone, ch.ResolvedFQDN)
	cfg, err := loadConfig(ch.Config)
	if err != nil {
		return fmt.Errorf("unable to load config: %v", err)
	}

	ociDNSClient, err := c.ociDNSClient(&cfg, ch.ResourceNamespace)
	if err != nil {
		return fmt.Errorf("unable to initialize ociDNSClient: %v", err)
	}

	ctx := context.Background()

	req := patchRequest(&cfg, ch, dns.RecordOperationOperationAdd)
	_, err = ociDNSClient.PatchZoneRecords(ctx, req)
	if err != nil {
		return fmt.Errorf("can not create TXT record: %v", err)
	}
	return nil
}

func (c *ociDNSProviderSolver) CleanUp(ch *v1alpha1.ChallengeRequest) error {
	klog.V(6).Infof("call function CleanUp: namespace=%s, zone=%s, fqdn=%s", ch.ResourceNamespace, ch.ResolvedZone, ch.ResolvedFQDN)
	cfg, err := loadConfig(ch.Config)
	if err != nil {
		return fmt.Errorf("unable to load config: %v", err)
	}

	ociDNSClient, err := c.ociDNSClient(&cfg, ch.ResourceNamespace)
	if err != nil {
		return fmt.Errorf("unable to initialize ociDNSClient: %v", err)
	}

	ctx := context.Background()

	req := patchRequest(&cfg, ch, dns.RecordOperationOperationRemove)
	_, err = ociDNSClient.PatchZoneRecords(ctx, req)
	if err != nil {
		return fmt.Errorf("can not delete TXT record: %v", err)
	}
	return nil
}

func (c *ociDNSProviderSolver) Initialize(kubeClientConfig *rest.Config, stopCh <-chan struct{}) error {
	cl, err := kubernetes.NewForConfig(kubeClientConfig)
	if err != nil {
		return err
	}

	c.client = cl
	return nil
}

func loadConfig(cfgJSON *extapi.JSON) (ociDNSProviderConfig, error) {
	cfg := ociDNSProviderConfig{}
	// handle the 'base case' where no configuration has been provided
	if cfgJSON == nil {
		return cfg, nil
	}
	if err := json.Unmarshal(cfgJSON.Raw, &cfg); err != nil {
		return cfg, fmt.Errorf("error decoding solver config: %v", err)
	}

	return cfg, nil
}

func patchRequest(cfg *ociDNSProviderConfig, ch *v1alpha1.ChallengeRequest, operation dns.RecordOperationOperationEnum) dns.PatchZoneRecordsRequest {
	resolvedZone := strings.TrimSuffix(ch.ResolvedZone, ".")
	domain := strings.TrimSuffix(ch.ResolvedFQDN, ".")
	rtype := "TXT"
	ttl := 60

	return dns.PatchZoneRecordsRequest{
		CompartmentId: &cfg.CompartmentOCID,
		ZoneNameOrId:  &resolvedZone,

		PatchZoneRecordsDetails: dns.PatchZoneRecordsDetails{
			Items: []dns.RecordOperation{
				{
					Domain:    &domain,
					Rtype:     &rtype,
					Rdata:     &ch.Key,
					Ttl:       &ttl,
					Operation: operation,
				},
			},
		},
		RequestMetadata: getRequestMetadataWithDefaultRetryPolicy(),
	}
}

// ociDNSClient is a helper function to initialize a DNS client from the oci-sdk
func (c *ociDNSProviderSolver) ociDNSClient(cfg *ociDNSProviderConfig, namespace string) (*dns.DnsClient, error) {
	secretName := cfg.OCIProfileSecretRef
	klog.V(6).Infof("Trying to load oci profile from secret `%s` in namespace `%s`", secretName, namespace)
	ctx := context.Background()
	sec, err := c.client.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to get secret `%s/%s`; %v", secretName, namespace, err)
	}

	tenancy, err := stringFromSecretData(&sec.Data, "tenancy")
	if err != nil {
		return nil, fmt.Errorf("unable to get tenancy from secret `%s/%s`; %v", secretName, namespace, err)
	}

	user, err := stringFromSecretData(&sec.Data, "user")
	if err != nil {
		return nil, fmt.Errorf("unable to get user from secret `%s/%s`; %v", secretName, namespace, err)
	}

	region, err := stringFromSecretData(&sec.Data, "region")
	if err != nil {
		return nil, fmt.Errorf("unable to get region from secret `%s/%s`; %v", secretName, namespace, err)
	}

	fingerprint, err := stringFromSecretData(&sec.Data, "fingerprint")
	if err != nil {
		return nil, fmt.Errorf("unable to get fingerprint from secret `%s/%s`; %v", secretName, namespace, err)
	}

	privateKey, err := stringFromSecretData(&sec.Data, "privateKey")
	if err != nil {
		return nil, fmt.Errorf("unable to get privateKey from secret `%s/%s`; %v", secretName, namespace, err)
	}

	privateKeyPassphrase, err := stringFromSecretData(&sec.Data, "privateKeyPassphrase")
	if err != nil {
		return nil, fmt.Errorf("unable to get privateKeyPassphrase from secret `%s/%s`; %v", secretName, namespace, err)
	}

	configProvider := common.NewRawConfigurationProvider(tenancy, user, region, fingerprint, privateKey, &privateKeyPassphrase)

	dnsClient, err := dns.NewDnsClientWithConfigurationProvider(configProvider)
	if err != nil {
		return nil, err
	}
	return &dnsClient, nil
}

func stringFromSecretData(secretData *map[string][]byte, key string) (string, error) {
	bytes, ok := (*secretData)[key]
	if !ok {
		return "", fmt.Errorf("key %q not found in secret data", key)
	}
	return string(bytes), nil
}

func getRequestMetadataWithDefaultRetryPolicy() common.RequestMetadata {
	return common.RequestMetadata{
		RetryPolicy: getDefaultRetryPolicy(),
	}
}

func getDefaultRetryPolicy() *common.RetryPolicy {
	// how many times to do the retry
	attempts := uint(10)

	// retry for all non-200 status code
	retryOnAllNon200ResponseCodes := func(r common.OCIOperationResponse) bool {
		response := r.Response.HTTPResponse()
		retry := !((r.Error == nil && 199 < response.StatusCode && response.StatusCode < 300) || response.StatusCode == 401)
		if retry {
			klog.V(6).Infof("request %s %s responded %s; retrying...", response.Request.Method, response.Request.URL.String(), response.Status)
		}
		return retry
	}
	return getExponentialBackoffRetryPolicy(attempts, retryOnAllNon200ResponseCodes)
}

func getExponentialBackoffRetryPolicy(n uint, fn func(r common.OCIOperationResponse) bool) *common.RetryPolicy {
	// the duration between each retry operation, you might want to waite longer each time the retry fails
	exponentialBackoff := func(r common.OCIOperationResponse) time.Duration {
		response := r.Response.HTTPResponse()
		duration := time.Duration(math.Pow(float64(2), float64(r.AttemptNumber-1))) * time.Second
		klog.V(6).Infof("backing off %s to retry %s %s after %d attempts", duration, response.Request.Method, response.Request.URL.String(), r.AttemptNumber)
		return duration
	}
	policy := common.NewRetryPolicy(n, fn, exponentialBackoff)
	return &policy
}

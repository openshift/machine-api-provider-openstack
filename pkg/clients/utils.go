package clients

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/utils/openstack/clientconfig"
	machinev1alpha1 "github.com/openshift/api/machine/v1alpha1"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/machine-api-provider-openstack/version"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"
)

// GetCloud fetches cloud credentials from a secret and return a parsed Cloud structure and optional CA cert
func GetCloud(kubeClient kubernetes.Interface, machine *machinev1.Machine) (clientconfig.Cloud, []byte, error) {
	cloud := clientconfig.Cloud{}
	machineSpec, err := MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return cloud, nil, fmt.Errorf("Failed to get Machine Spec from Provider Spec: %v", err)
	}

	if machineSpec.CloudsSecret == nil || machineSpec.CloudsSecret.Name == "" {
		return cloud, nil, fmt.Errorf("Cloud secret name can't be empty")
	}

	namespace := machineSpec.CloudsSecret.Namespace
	if namespace == "" {
		namespace = machine.Namespace
	}
	cloud, cacert, err := getCloudFromSecret(kubeClient, namespace, machineSpec.CloudsSecret.Name, machineSpec.CloudName)
	if err != nil {
		return cloud, cacert, fmt.Errorf("Failed to get cloud from secret: %v", err)
	}

	return cloud, cacert, nil
}

// GetProviderClient returns an authenticated provider client based on values in the cloud structure
func GetProviderClient(cloud clientconfig.Cloud, cert []byte) (*gophercloud.ProviderClient, error) {
	clientOpts := new(clientconfig.ClientOpts)

	if cloud.AuthInfo != nil {
		clientOpts.AuthInfo = cloud.AuthInfo
		clientOpts.AuthType = cloud.AuthType
		clientOpts.Cloud = cloud.Cloud
		clientOpts.RegionName = cloud.RegionName
	}

	opts, err := clientconfig.AuthOptions(clientOpts)

	if err != nil {
		return nil, err
	}

	opts.AllowReauth = true

	provider, err := openstack.NewClient(opts.IdentityEndpoint)
	if err != nil {
		return nil, fmt.Errorf("Create new provider client failed: %v", err)
	}

	// we represent version using commits since we don't tag releases
	ua := gophercloud.UserAgent{}
	ua.Prepend(fmt.Sprintf("machine-api-provider-openstack/%s", version.Get().GitCommit))
	provider.UserAgent = ua

	if cert != nil {
		certPool, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("Create system cert pool failed: %v", err)
		}
		certPool.AppendCertsFromPEM(cert)
		client := http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					RootCAs: certPool,
				},
				Proxy: http.ProxyFromEnvironment,
			},
		}
		provider.HTTPClient = client
	} else {
		klog.Infof("Cloud provider CA cert not provided, using system trust bundle")
	}

	err = openstack.Authenticate(provider, *opts)
	if err != nil {
		return nil, fmt.Errorf("Failed to authenticate provider client: %v", err)
	}

	return provider, nil
}

func getCloudFromSecret(kubeClient kubernetes.Interface, namespace string, secretName string, cloudName string) (clientconfig.Cloud, []byte, error) {
	emptyCloud := clientconfig.Cloud{}

	if secretName == "" {
		return emptyCloud, nil, nil
	}

	if secretName != "" && cloudName == "" {
		return emptyCloud, nil, fmt.Errorf("Secret name set to %v but no cloud was specified. Please set cloud_name in your machine spec.", secretName)
	}

	secret, err := kubeClient.CoreV1().Secrets(namespace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		return emptyCloud, nil, fmt.Errorf("Failed to get secrets %s/%s from kubernetes api: %v", namespace, secretName, err)
	}

	content, ok := secret.Data["clouds.yaml"]
	if !ok {
		return emptyCloud, nil, fmt.Errorf("OpenStack credentials secret %v did not contain key %v", secretName, "clouds.yaml")
	}
	var clouds clientconfig.Clouds
	err = yaml.Unmarshal(content, &clouds)
	if err != nil {
		return emptyCloud, nil, fmt.Errorf("failed to unmarshal clouds credentials stored in secret %v: %v", secretName, err)
	}

	var cacert []byte
	content, ok = secret.Data["cacert"]
	if ok {
		cacert = []byte(content)
	} else {
		// Fallback for retrieving CA cert from the CCM config. Starting in
		// OCP 4.19, cloud-credential-operator provides this in the credential
		// secret, as seen above, so this is no longer necessary outside of
		// upgrade scenarios.
		// TODO(stephenfin): Remove in 4.20
		cacert = getCACertFromConfig(kubeClient)
	}

	return clouds.Clouds[cloudName], cacert, nil
}

// getCACertFromConfig gets the CA certificate from the CCM configmap
func getCACertFromConfig(kubeClient kubernetes.Interface) []byte {
	cloudConfig, err := kubeClient.CoreV1().ConfigMaps("openshift-config").Get(context.TODO(), "cloud-provider-config", metav1.GetOptions{})
	if err != nil {
		klog.Warningf("failed to get configmap openshift-config/cloud-provider-config from kubernetes api: %v", err)
		return nil
	}

	if cacert, ok := cloudConfig.Data["ca-bundle.pem"]; ok {
		return []byte(cacert)
	}

	return nil
}

// MachineSpecFromProviderSpec unmarshals a provider status into an OpenStack Machine Status type
func MachineSpecFromProviderSpec(providerSpec machinev1.ProviderSpec) (*machinev1alpha1.OpenstackProviderSpec, error) {
	if providerSpec.Value == nil {
		return nil, errors.New("no such providerSpec found in manifest")
	}

	var config machinev1alpha1.OpenstackProviderSpec
	if err := yaml.Unmarshal(providerSpec.Value.Raw, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

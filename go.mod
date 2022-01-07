module github.com/openshift/machine-api-provider-openstack

go 1.15

require (
	github.com/ajeddeloh/go-json v0.0.0-20170920214419-6a2fe990e083 // indirect
	github.com/ajeddeloh/yaml v0.0.0-20170912190910-6b94386aeefd // indirect
	github.com/coreos/container-linux-config-transpiler v0.9.0
	github.com/coreos/go-systemd v0.0.0-20190620071333-e64a0ec8b42a // indirect
	github.com/coreos/ignition v0.33.0 // indirect
	github.com/go-logr/logr v1.2.2
	github.com/gophercloud/gophercloud v0.16.0
	github.com/gophercloud/utils v0.0.0-20210323225332-7b186010c04f
	github.com/onsi/ginkgo v1.16.5
	github.com/onsi/gomega v1.17.0
	github.com/openshift/api v0.0.0-20211222145011-3bf13cf5081a
	github.com/openshift/client-go v0.0.0-20211209144617-7385dd6338e3
	github.com/openshift/machine-api-operator v0.2.1-0.20211223185609-7ba373c29f8f
	github.com/vincent-petithory/dataurl v0.0.0-20160330182126-9a301d65acbb // indirect
	go4.org v0.0.0-20191010144846-132d2879e1e9 // indirect
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.23.1
	k8s.io/apimachinery v0.23.1
	k8s.io/client-go v0.23.0
	k8s.io/cluster-bootstrap v0.23.1
	k8s.io/klog/v2 v2.30.0
	sigs.k8s.io/cluster-api v1.0.2
	sigs.k8s.io/cluster-api-provider-openstack v0.5.1-0.20211214023603-b936d98a83d6
	sigs.k8s.io/controller-runtime v0.11.0
	sigs.k8s.io/yaml v1.3.0
)

replace sigs.k8s.io/cluster-api => sigs.k8s.io/cluster-api v1.0.2

module github.com/openshift/machine-api-provider-openstack

go 1.15

require (
	github.com/ajeddeloh/go-json v0.0.0-20170920214419-6a2fe990e083 // indirect
	github.com/ajeddeloh/yaml v0.0.0-20170912190910-6b94386aeefd // indirect
	github.com/coreos/container-linux-config-transpiler v0.9.0
	github.com/coreos/go-systemd v0.0.0-20190620071333-e64a0ec8b42a // indirect
	github.com/coreos/ignition v0.33.0 // indirect
	github.com/go-logr/logr v0.4.0
	github.com/gophercloud/gophercloud v0.16.0
	github.com/gophercloud/utils v0.0.0-20210323225332-7b186010c04f
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.15.0
	github.com/openshift/api v0.0.0-20211108165917-be1be0e89115
	github.com/openshift/client-go v0.0.0-20211025111749-96ca2abfc56c
	github.com/openshift/machine-api-operator v0.2.1-0.20211111133920-c8bba3e64310
	github.com/vincent-petithory/dataurl v0.0.0-20160330182126-9a301d65acbb // indirect
	go4.org v0.0.0-20191010144846-132d2879e1e9 // indirect
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.22.1
	k8s.io/apimachinery v0.22.1
	k8s.io/client-go v0.22.1
	k8s.io/cluster-bootstrap v0.21.1
	k8s.io/klog v1.0.0
	k8s.io/klog/v2 v2.9.0
	sigs.k8s.io/cluster-api v0.4.3
	sigs.k8s.io/cluster-api-provider-openstack v0.4.1-0.20211011120440-eb83033a0bca
	sigs.k8s.io/controller-runtime v0.9.7
	sigs.k8s.io/yaml v1.2.0
)

replace sigs.k8s.io/cluster-api => sigs.k8s.io/cluster-api v0.4.0-beta.1

/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/openshift/machine-api-provider-openstack/pkg/machine"
	"github.com/openshift/machine-api-provider-openstack/pkg/machineset"
	"github.com/openshift/machine-api-provider-openstack/version"

	configv1 "github.com/openshift/api/config/v1"
	machinev1alpha1 "github.com/openshift/api/machine/v1alpha1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	maoMachine "github.com/openshift/machine-api-operator/pkg/controller/machine"
	"github.com/openshift/machine-api-operator/pkg/metrics"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/klogr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	rTcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

// The default durations for the leader election operations.
var (
	leaseDuration = 120 * time.Second
	renewDeadline = 110 * time.Second
	retryPeriod   = 20 * time.Second
	syncPeriod    = 1 * time.Hour
)

func main() {

	flag.Set("logtostderr", "true")
	watchNamespace := flag.String(
		"namespace",
		"",
		"Namespace that the controller watches to reconcile machine-api objects. If unspecified, the controller watches for machine-api objects across all namespaces.",
	)

	healthAddr := flag.String(
		"health-addr",
		":9440",
		"The address for health checking.",
	)

	leaderElectResourceNamespace := flag.String(
		"leader-elect-resource-namespace",
		"",
		"The namespace of resource object that is used for locking during leader election. If unspecified and running in cluster, defaults to the service account namespace for the controller. Required for leader-election outside of a cluster.",
	)

	leaderElect := flag.Bool(
		"leader-elect",
		false,
		"Start a leader election client and gain leadership before executing the main loop. Enable this when running replicated components for high availability.",
	)

	leaderElectLeaseDuration := flag.Duration(
		"leader-elect-lease-duration",
		leaseDuration,
		"The duration that non-leader candidates will wait after observing a leadership renewal until attempting to acquire leadership of a led but unrenewed leader slot. This is effectively the maximum duration that a leader can be stopped before it is replaced by another candidate. This is only applicable if leader election is enabled.",
	)
	metricsAddress := flag.String(
		"metrics-bind-address",
		metrics.DefaultMachineMetricsAddress,
		"Address for hosting metrics",
	)

	showVersion := flag.Bool(
		"version",
		false,
		"Show current version",
	)

	klog.InitFlags(nil)
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Get())
		fmt.Println(version.Get().GitCommit)
		os.Exit(0)
	}

	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err != nil {
		klog.Fatal(err)
	}

	// Setup a Manager
	opts := manager.Options{
		HealthProbeBindAddress:  *healthAddr,
		LeaderElection:          *leaderElect,
		LeaderElectionNamespace: *leaderElectResourceNamespace,
		LeaderElectionID:        "cluster-api-provider-openstack-leader",
		LeaseDuration:           leaderElectLeaseDuration,
		MetricsBindAddress:      *metricsAddress,
		// Slow the default retry and renew election rate to reduce etcd writes at idle: BZ 1858400
		RetryPeriod:   &retryPeriod,
		RenewDeadline: &renewDeadline,
		SyncPeriod:    &syncPeriod,
	}
	if *watchNamespace != "" {
		opts.Namespace = *watchNamespace
		klog.Infof("Watching machine-api objects only in namespace %q for reconciliation.", opts.Namespace)
	}

	mgr, err := manager.New(cfg, opts)
	if err != nil {
		klog.Fatal(err)
	}

	klog.Infof("Initializing Dependencies.")

	// Setup Scheme for all resources
	if err := machinev1alpha1.Install(mgr.GetScheme()); err != nil {
		klog.Fatal(err)
	}

	if err := configv1.AddToScheme(mgr.GetScheme()); err != nil {
		klog.Fatal(err)
	}

	if err := machinev1beta1.AddToScheme(mgr.GetScheme()); err != nil {
		klog.Fatal(err)
	}

	params := getActuatorParams(mgr)
	machineActuator, err := machine.NewActuator(params)
	if err != nil {
		klog.Fatal(err)
	}

	// Setup OpenStack Machine controller
	if err := maoMachine.AddWithActuator(mgr, machineActuator); err != nil {
		klog.Fatal(err)
	}

	// Setup OpenStack MachineSet controller
	ctrl.SetLogger(klogr.New())
	setupLog := ctrl.Log.WithName("setup")
	if err = (&machineset.Reconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("MachineSet"),
	}).SetupWithManager(mgr, rTcontroller.Options{}); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "MachineSet")
		os.Exit(1)
	}

	if err := mgr.AddReadyzCheck("ping", healthz.Ping); err != nil {
		klog.Fatal(err)
	}

	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		klog.Fatal(err)
	}

	log.Printf("Starting the Cmd.")

	// Start the Cmd
	log.Fatal(mgr.Start(signals.SetupSignalHandler()))
}

func getActuatorParams(mgr manager.Manager) machine.ActuatorParams {
	config := mgr.GetConfig()

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Could not create kubernetes client to talk to the apiserver: %v", err)
	}
	configClient, err := configclient.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create a config client to talk to the apiserver: %v", err)
	}

	return machine.ActuatorParams{
		Client:        mgr.GetClient(),
		KubeClient:    kubeClient,
		ConfigClient:  configClient,
		Scheme:        mgr.GetScheme(),
		EventRecorder: mgr.GetEventRecorderFor("openstack_controller"),
	}

}

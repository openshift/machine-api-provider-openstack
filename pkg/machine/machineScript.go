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

package machine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"text/template"

	openstackconfigv1 "github.com/openshift/machine-api-provider-openstack/pkg/apis/openstackproviderconfig/v1alpha1"
	"github.com/openshift/machine-api-provider-openstack/pkg/bootstrap"

	clconfig "github.com/coreos/container-linux-config-transpiler/config"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	UserDataKey          = "userData"
	DisableTemplatingKey = "disableTemplating"
	PostprocessorKey     = "postprocessor"
)

type setupParams struct {
	Token       string
	Machine     *machinev1.Machine
	MachineSpec *openstackconfigv1.OpenstackProviderSpec
}

func init() {
}

func masterStartupScript(machine *machinev1.Machine, script string) (string, error) {
	machineSpec, err := openstackconfigv1.MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return "", err
	}

	params := setupParams{
		Machine:     machine,
		MachineSpec: machineSpec,
	}

	masterStartUpScript := template.Must(template.New("masterStartUp").Parse(script))

	var buf bytes.Buffer
	if err := masterStartUpScript.Execute(&buf, params); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func nodeStartupScript(machine *machinev1.Machine, token, script string) (string, error) {
	machineSpec, err := openstackconfigv1.MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return "", err
	}

	params := setupParams{
		Token:       token,
		Machine:     machine,
		MachineSpec: machineSpec,
	}

	nodeStartUpScript := template.Must(template.New("nodeStartUp").Parse(script))

	var buf bytes.Buffer
	if err := nodeStartUpScript.Execute(&buf, params); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (oc *OpenstackClient) getUserData(machine *machinev1.Machine, providerSpec *openstackconfigv1.OpenstackProviderSpec, kubeClient kubernetes.Interface) (string, error) {
	// get machine startup script
	var ok bool
	var disableTemplating bool
	var postprocessor string
	var postprocess bool

	userData := []byte{}
	if providerSpec.UserDataSecret != nil {
		namespace := providerSpec.UserDataSecret.Namespace
		if namespace == "" {
			namespace = machine.Namespace
		}

		if providerSpec.UserDataSecret.Name == "" {
			return "", fmt.Errorf("UserDataSecret name must be provided")
		}

		userDataSecret, err := kubeClient.CoreV1().Secrets(namespace).Get(context.TODO(), providerSpec.UserDataSecret.Name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}

		userData, ok = userDataSecret.Data[UserDataKey]
		if !ok {
			return "", fmt.Errorf("Machine's userdata secret %v in namespace %v did not contain key %v", providerSpec.UserDataSecret.Name, namespace, UserDataKey)
		}

		_, disableTemplating = userDataSecret.Data[DisableTemplatingKey]

		var p []byte
		p, postprocess = userDataSecret.Data[PostprocessorKey]

		postprocessor = string(p)
	}

	var userDataRendered string
	var err error
	if len(userData) > 0 && !disableTemplating {
		// FIXME(mandre) Find the right way to check if machine is part of the control plane
		if machine.ObjectMeta.Name != "" {
			userDataRendered, err = masterStartupScript(machine, string(userData))
			if err != nil {
				return "", fmt.Errorf("error creating Openstack instance: %v", err)
			}
		} else {
			klog.Info("Creating bootstrap token")
			token, err := bootstrap.CreateBootstrapToken(oc.client)
			if err != nil {
				return "", fmt.Errorf("error creating Openstack instance: %v", err)
			}
			userDataRendered, err = nodeStartupScript(machine, token, string(userData))
			if err != nil {
				return "", fmt.Errorf("error creating Openstack instance: %v", err)
			}
		}
	} else {
		userDataRendered = string(userData)
	}

	if postprocess {
		switch postprocessor {
		// Postprocess with the Container Linux ct transpiler.
		case "ct":
			clcfg, ast, report := clconfig.Parse([]byte(userDataRendered))
			if len(report.Entries) > 0 {
				return "", fmt.Errorf("Postprocessor error: %s", report.String())
			}

			ignCfg, report := clconfig.Convert(clcfg, "openstack-metadata", ast)
			if len(report.Entries) > 0 {
				return "", fmt.Errorf("Postprocessor error: %s", report.String())
			}

			ud, err := json.Marshal(&ignCfg)
			if err != nil {
				return "", fmt.Errorf("Postprocessor error: %s", err)
			}

			userDataRendered = string(ud)

		default:
			return "", fmt.Errorf("Postprocessor error: unknown postprocessor: '%s'", postprocessor)
		}
	}

	return userDataRendered, nil
}

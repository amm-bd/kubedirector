// Copyright 2018 BlueData Software, Inc.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at

//     http://www.apache.org/licenses/LICENSE-2.0

// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package executor

import (
	"encoding/json"

	kdv1 "github.com/bluek8s/kubedirector/pkg/apis/kubedirector.bluedata.io/v1alpha1"
	"github.com/bluek8s/kubedirector/pkg/catalog"
	"github.com/bluek8s/kubedirector/pkg/shared"
	"github.com/operator-framework/operator-sdk/pkg/sdk"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// CreateHeadlessService creates in k8s the "cluster service" used for
// intra-cluster network communication and for defining the virtual cluster's
// DNS subdomain. Cluster service name is an important part of DNS identity,
// so if we had already used a name previously and are re-creating the service,
// re-use that same name instead of generating a new one.
func CreateHeadlessService(
	cr *kdv1.KubeDirectorCluster,
) (*v1.Service, error) {

	name := headlessServiceName
	service := &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       cr.Namespace,
			OwnerReferences: ownerReferences(cr),
			Labels:          labelsForService(cr),
		},
		Spec: v1.ServiceSpec{
			ClusterIP: "None",
			Selector: map[string]string{
				headlessServiceLabel: name + "-" + cr.Name,
			},
			Ports: []v1.ServicePort{
				{
					Name: "port",
					Port: 8888, // not used
				},
			},
		},
	}
	if cr.Status.ClusterService == "" {
		service.ObjectMeta.GenerateName = name + "-"
	} else {
		service.ObjectMeta.Name = cr.Status.ClusterService
	}
	err := sdk.Create(service)

	return service, err
}

// UpdateHeadlessService examines the current cluster service in k8s and may
// take steps to reconcile it to the desired spec.
func UpdateHeadlessService(
	cr *kdv1.KubeDirectorCluster,
	service *v1.Service,
) error {

	// TBD: We could compare the service against the expected service
	// (generated from the CR) and if there is a deviance in properties that
	// we need/expect to be under our control, correct them here. Not going
	// to tackle that at first.

	return nil
}

// CreatePodService creates in k8s a service that exposes the designated
// service endpoints of a virtual cluster member. Depending on the app type
// definition, this will be either a NodePort service (default) or a
// LoadBalancer service. If there are no ports to configure for this service,
// no service object will be created and the function will return (nil, nil).
func CreatePodService(
	cr *kdv1.KubeDirectorCluster,
	role *kdv1.Role,
	podName string,
) (*v1.Service, error) {

	serviceType := serviceType(*cr.Spec.ServiceType)

	portInfoList, portsErr := catalog.PortsForRole(cr, role.Name)
	if portsErr != nil {
		return nil, portsErr
	}
	if len(portInfoList) == 0 {
		return nil, nil
	}
	service := &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            serviceName(podName),
			Namespace:       cr.Namespace,
			OwnerReferences: ownerReferences(cr),
			Labels:          labelsForService(cr),
		},
		Spec: v1.ServiceSpec{
			Selector: labelsForPod(cr, role, podName),
			Type:     serviceType,
		},
	}
	for _, portInfo := range portInfoList {
		servicePort := v1.ServicePort{
			Port: portInfo.Port,
			Name: portInfo.ID,
		}
		service.Spec.Ports = append(service.Spec.Ports, servicePort)
	}
	createErr := sdk.Create(service)
	return service, createErr
}

// UpdatePodService examines a current per-member service in k8s and may take
// steps to reconcile it to the desired spec.
// TBD: Currently this function handles changes only for serviceType, and is
// only called if the service is known to already exist. If port-changing is
// supported in the future, either this function or its caller must take care
// of possibly transitioning to and from the "no ports" state which will
// involve deleting or creating the service object rather than just modifying.
func UpdatePodService(
	cr *kdv1.KubeDirectorCluster,
	role *kdv1.Role,
	podName string,
	service *v1.Service,
) error {

	reqServiceType := serviceType(*cr.Spec.ServiceType)

	// Compare cluster CR's service type against created service
	if reqServiceType == service.Spec.Type {
		return nil
	}

	shared.LogInfof(
		cr,
		shared.EventReasonMember,
		"modifying serviceType from %s to %s for service{%s}",
		service.Spec.Type,
		reqServiceType,
		service.Name,
	)
	// serviceType has been updated. Create a patch to change the type
	type patchSpec struct {
		Op    string         `json:"op"`
		Path  string         `json:"path"`
		Value v1.ServiceType `json:"value"`
	}

	patch := []patchSpec{
		{
			Op:    "replace",
			Path:  "/spec/type",
			Value: reqServiceType,
		},
	}

	patchBytes, patchErr := json.Marshal(patch)
	if patchErr == nil {
		patchErr = sdk.Patch(service, types.JSONPatchType, patchBytes)
	}

	return patchErr
}

// DeletePodService deletes a per-member service from k8s.
func DeletePodService(
	namespace string,
	serviceName string,
) error {

	toDelete := &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
		},
	}

	return sdk.Delete(toDelete)
}

// serviceName is a utility function for generating the name of a service
// from a given base string.
func serviceName(
	baseName string,
) string {

	return "svc-" + baseName
}

// serviceType is a utility function that converts serviceType string to
// v1.ServiceType
func serviceType(
	crServicetype string,
) v1.ServiceType {

	if crServicetype == string(v1.ServiceTypeNodePort) {
		return v1.ServiceTypeNodePort
	}

	return v1.ServiceTypeLoadBalancer
}

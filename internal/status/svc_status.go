/*
 * Copyright 2019-2020 VMware, Inc.
 * All Rights Reserved.
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You may obtain a copy of the License at
*   http://www.apache.org/licenses/LICENSE-2.0
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
*/

package status

import (
	"strings"

	avicache "github.com/vmware/load-balancer-and-ingress-services-for-kubernetes/internal/cache"
	"github.com/vmware/load-balancer-and-ingress-services-for-kubernetes/internal/lib"
	"github.com/vmware/load-balancer-and-ingress-services-for-kubernetes/pkg/utils"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func UpdateL4LBStatus(options []UpdateStatusOptions, bulk bool) {
	mClient := utils.GetInformers().ClientSet
	var servicesToUpdate []string
	var updateServiceOptions []UpdateStatusOptions

	for _, option := range options {
		// service TypeLB would have just one NamespaceServiceName considering one VS per svcLB
		// does not apply for svcLBs exposed via gateways
		service := option.ServiceMetadata.NamespaceServiceName[0]
		if len(option.ServiceMetadata.HostNames) != 1 && !lib.GetAdvancedL4() {
			utils.AviLog.Error("Service hostname not found for service %s status update", service)
			continue
		}

		option.IngSvc = service
		servicesToUpdate = append(servicesToUpdate, service)
		updateServiceOptions = append(updateServiceOptions, option)
	}

	serviceMap := getServices(servicesToUpdate, bulk)
	for _, option := range updateServiceOptions {
		key, svcMetadata := option.Key, option.ServiceMetadata
		if service := serviceMap[option.IngSvc]; service != nil {
			oldServiceStatus := service.Status.LoadBalancer.DeepCopy()
			if option.Vip == "" {
				// nothing to do here
				continue
			}

			var svcHostname string
			if len(svcMetadata.HostNames) > 0 {
				svcHostname = svcMetadata.HostNames[0]
			}
			service.Status = corev1.ServiceStatus{
				LoadBalancer: corev1.LoadBalancerStatus{
					Ingress: []corev1.LoadBalancerIngress{corev1.LoadBalancerIngress{
						IP:       option.Vip,
						Hostname: svcHostname,
					}}}}

			if sameStatus := compareLBStatus(oldServiceStatus, &service.Status.LoadBalancer); sameStatus {
				utils.AviLog.Debugf("key: %s, msg: No changes detected in service status. old: %+v new: %+v",
					key, oldServiceStatus.Ingress, service.Status.LoadBalancer.Ingress)
				continue
			}

			_, err := mClient.CoreV1().Services(service.Namespace).UpdateStatus(service)
			if err != nil {
				utils.AviLog.Errorf("key: %s, msg: there was an error in updating the loadbalancer status: %v", key, err)
				continue
			}
			utils.AviLog.Infof("key: %s, msg: Successfully updated the status of serviceLB: %s old: %+v new %+v",
				key, option.IngSvc, oldServiceStatus.Ingress, service.Status.LoadBalancer.Ingress)
		}
	}

	return
}

func DeleteL4LBStatus(svc_mdata_obj avicache.ServiceMetadataObj, key string) error {
	mClient := utils.GetInformers().ClientSet
	serviceNSName := strings.Split(svc_mdata_obj.NamespaceServiceName[0], "/")
	mLb, err := mClient.CoreV1().Services(serviceNSName[0]).Get(serviceNSName[1], metav1.GetOptions{})
	if err != nil {
		utils.AviLog.Warnf("key: %s, msg: there was a problem in resetting the service status: %s", key, err)
		return err
	}
	mLb.Status = corev1.ServiceStatus{
		LoadBalancer: corev1.LoadBalancerStatus{
			Ingress: []corev1.LoadBalancerIngress{},
		},
	}
	_, err = mClient.CoreV1().Services(serviceNSName[0]).UpdateStatus(mLb)
	if err != nil {
		utils.AviLog.Errorf("key: %s, msg: there was an error in resetting the loadbalancer status: %v", key, err)
		return err
	}

	utils.AviLog.Infof("key: %s, msg: Successfully reset the status of serviceLB: %s", key, svc_mdata_obj.NamespaceServiceName[0])
	return nil
}

// getServices fetches all serviceLB and returns a map: {"namespace/name": serviceObj...}
// if bulk is set to true, this fetches all services in a single k8s api-server call
func getServices(serviceNSNames []string, bulk bool, retryNum ...int) map[string]*corev1.Service {
	retry := 0
	mClient := utils.GetInformers().ClientSet
	serviceMap := make(map[string]*corev1.Service)
	if len(retryNum) > 0 {
		utils.AviLog.Infof("msg: Retrying to get the services for status update")
		retry = retryNum[0]
		if retry >= 2 {
			utils.AviLog.Errorf("msg: getServices for status update retried 3 times, aborting")
			return serviceMap
		}
	}

	if bulk {
		serviceLBList, err := mClient.CoreV1().Services("").List(metav1.ListOptions{})
		if err != nil {
			utils.AviLog.Warnf("Could not get the service object for UpdateStatus: %s", err)
			// retry get if request timeout
			if strings.Contains(err.Error(), utils.K8S_ETIMEDOUT) {
				return getServices(serviceNSNames, bulk, retry+1)
			}
		}
		for i := range serviceLBList.Items {
			ing := serviceLBList.Items[i]
			serviceMap[ing.Namespace+"/"+ing.Name] = &ing
		}

		return serviceMap
	}

	for _, namespaceName := range serviceNSNames {
		nsNameSplit := strings.Split(namespaceName, "/")
		serviceLB, err := mClient.CoreV1().Services(nsNameSplit[0]).Get(nsNameSplit[1], metav1.GetOptions{})
		if err != nil {
			utils.AviLog.Warnf("msg: Could not get the service object for UpdateStatus: %s", err)
			// retry get if request timeout
			if strings.Contains(err.Error(), utils.K8S_ETIMEDOUT) {
				return getServices(serviceNSNames, bulk, retry+1)
			}
			continue
		}

		serviceMap[serviceLB.Namespace+"/"+serviceLB.Name] = serviceLB
	}

	return serviceMap
}

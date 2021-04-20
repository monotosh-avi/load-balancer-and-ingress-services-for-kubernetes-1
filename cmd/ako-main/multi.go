package main

import (
	"strings"

	oshiftclient "github.com/openshift/client-go/route/clientset/versioned"
	"github.com/vmware/load-balancer-and-ingress-services-for-kubernetes/internal/k8s"
	"github.com/vmware/load-balancer-and-ingress-services-for-kubernetes/internal/lib"
	"github.com/vmware/load-balancer-and-ingress-services-for-kubernetes/pkg/utils"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type KubeClusterDetails struct {
	clusterName string
	kubeconfig  string
	kubeapi     string
	informers   *utils.Informers
}

type MemberCluster struct {
	ClusterContext string
	kubeconfig     string
	kubeapi        string
	informers      *utils.Informers
}

func InitializeMemberClusters(membersKubeConfig string, memberClusters []string, stopCh <-chan struct{}) ([]*k8s.AviController, error) {
	clusterDetails := loadClusterAccess(membersKubeConfig, memberClusters)
	clients := make(map[string]*kubernetes.Clientset)

	aviCtrlList := make([]*k8s.AviController, 0)
	for _, cluster := range clusterDetails {
		//gslbutils.Logf("cluster: %s, msg: %s", cluster.clusterName, "initializing")
		cfg, err := BuildContextConfig(cluster.kubeconfig, cluster.clusterName)
		if err != nil {
			utils.AviLog.Warnf("cluster: %s, msg: %s, %s", cluster.clusterName, "error in connecting to kubernetes API", err)
			continue
		} else {
			utils.AviLog.Infof("cluster: %s, msg: %s", cluster.clusterName, "successfully connected to kubernetes API")
		}
		aviCtrl := InitializeMemberCluster(cfg, cluster, clients, stopCh)
		if aviCtrl != nil {
			aviCtrlList = append(aviCtrlList, aviCtrl)
		}
	}
	return aviCtrlList, nil
}

func loadClusterAccess(membersKubeConfig string, memberClusters []string) []KubeClusterDetails {
	var clusterDetails []KubeClusterDetails
	for _, memberCluster := range memberClusters {
		clusterDetails = append(clusterDetails, KubeClusterDetails{memberCluster,
			membersKubeConfig, "", nil})
		//gslbutils.Logf("cluster: %s, msg: %s", memberCluster.ClusterContext, "loaded cluster access")
	}
	return clusterDetails
}

func BuildContextConfig(kubeconfigPath, context string) (*restclient.Config, error) {
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{
			CurrentContext: context,
		}).ClientConfig()
}

func InitializeMemberCluster(cfg *restclient.Config, cluster KubeClusterDetails,
	clients map[string]*kubernetes.Clientset, stopCh <-chan struct{}) *k8s.AviController {

	informersArg := make(map[string]interface{})

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		//gslbutils.Warnf("cluster: %s, msg: %s, %s", cluster.clusterName, "error in creating kubernetes clientset",
		//	err)
		return nil
	}
	oshiftClient, err := oshiftclient.NewForConfig(cfg)
	if err != nil {
		//gslbutils.Warnf("cluster: %s, msg: %s, %s", cluster.clusterName, "error in creating openshift clientset")
		return nil
	}
	informersArg[utils.INFORMERS_OPENSHIFT_CLIENT] = oshiftClient
	informersArg[utils.INFORMERS_INSTANTIATE_ONCE] = false
	registeredInformers, err := lib.InformersToRegister(oshiftClient, kubeClient, cluster.clusterName)
	if err != nil {
		//gslbutils.Errf("error in initializing informers")
		return nil
	}
	if len(registeredInformers) == 0 {
		//gslbutils.Errf("No informers available for this cluster %s, returning", cluster.clusterName)
		return nil
	}
	//gslbutils.Logf("Informers for cluster %s: %v", cluster.clusterName, registeredInformers)
	informerInstance := utils.NewInformers(utils.KubeClientIntf{
		ClientSet: kubeClient},
		registeredInformers,
		informersArg)
	cName := strings.Replace(cluster.clusterName, "@", "-", 1)
	utils.SetInformersMultiCluster(cName, informerInstance)
	utils.AviLog.Infof("xxx setting informer for %v, %v", cluster.clusterName, informerInstance)
	clients[cluster.clusterName] = kubeClient

	var aviCtrl *k8s.AviController

	aviCtrl = k8s.GetK8sAviController(cluster.clusterName, informerInstance)
	aviCtrl.ClusterName = cName
	aviCtrl.Informers = informerInstance
	utils.AviLog.Infof("xxx clustername %v", aviCtrl.ClusterName)

	//gslbutils.AddClusterContext(cluster.clusterName)
	//aviCtrl.SetupEventHandlers(k8s.K8sinformers{Cs: clients[cluster.clusterName]})
	//aviCtrl.Start(stopCh)
	dynamicClient, err := lib.NewDynamicClientSet(cfg)
	k8sInfo := k8s.K8sinformers{Cs: kubeClient, DynamicClient: dynamicClient, OshiftClient: oshiftClient}
	aviCtrl.K8sInfo = k8sInfo
	return aviCtrl
}

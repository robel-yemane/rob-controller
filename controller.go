package main

import (
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	appsinformers "k8s.io/client-go/informers/apps/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

const controllerAgentName = "rob-controller"

const (
	//SuccessSynced is used as part of teh Event 'reason' when a Rob is synced
	SuccessSynced = "Synced"
	//ErrResourceExists is used as part of the Event 'reason' when a Rob fails
	// to sync due to a Deployment of the same name already existing.
	ErrResourceExists = "ErrResourceExists"

	// MessageResourceExists is the message used for Events when a resource
	// fails to sync due to a Deployment already existing
	MessageResourceExists = "Resource %q already exists and is not managed by Rob"
	// MessageResourcedSynced is the message used for an Event fired when a Rob
	// is synced successfully
	MessageResourceSynced = "Rob synced successfully"
)

// Controller is the controller implementation for Rob resources
type Controller struct {

	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	//robclientset is a clientset for our own API group
	robclientset clientset.Interface

	deploymentsLister appslisters.DeploymentsLister
	deploymentsSynced cache.InformerSynced
	robLister         listers.RobLister
	robsSynced        cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and maes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder
}

// NewController returns a new rob controller
func NewController(
	kubeclientset kubernetes.Interface,
	robclientset clientset.Interface,
	deploymentInformer appsinformers.DeploymentInformer,
	robInformer informers.RobInformer) *Controller {

	// Create event broadcaster
	// Add rob-controller types to the default Kubernetes Scheme so Events can be
	// logged for rob-controller types.
	utilruntime.Must(robscheme.AddToScheme(scheme.Scheme))

}

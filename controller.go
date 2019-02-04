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

	robv1alpha1 "github.com/robel-yemane/rob-controller/pkg/apis/robcontroller/v1alpha1"
	clientset "github.com/robel-yemane/rob-controller/pkg/client/clientset/versioned"
	robscheme "github.com/robel-yemane/rob-controller/pkg/client/clientset/versioned/scheme"
	informers "github.com/robel-yemane/rob-controller/pkg/client/informers/externalversions/robcontroller/v1alpha1"
	listers "github.com/robel-yemane/rob-controller/pkg/client/listers/robcontroller/v1alpha1"
)

const controllerAgentName = "rob-controller"

const (
	//SuccessSynced is used as part of the Event 'reason' when a Rob is synced
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

	deploymentsLister appslisters.DeploymentLister
	deploymentsSynced cache.InformerSynced
	robsLister        listers.RobLister
	robsSynced        cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
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
	klog.V(4).Info("Creating  event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	controller := &Controller{
		kubeclientset:     kubeclientset,
		robclientset:      robclientset,
		deploymentsLister: deploymentInformer.Lister(),
		deploymentsSynced: deploymentInformer.Informer().HasSynced,
		robsLister:        robInformer.Lister(),
		robsSynced:        robInformer.Informer().HasSynced,
		workqueue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Robs"),
		recorder:          recorder,
	}

	// TH: ResourceEventHandler: these are the callback functions which will be called by the Informer when it wants to deliver
	// an object to your (custom) controller. The typical pattern to write these functions is to obtain the dispatched object's
	// key and enqueue that key in a work queue for further processing.
	// this is done in enqueueRob.
	klog.Info("Setting up event handlers")
	// Set up an event handler for when Rob resources change
	robInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueRob,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueueRob(new)
		},
	})
	// Set up an event handler for when Deployment resources change. This
	// handler will lookup the owner of the given Deployment, and if it is
	// owned by a Rob resource will enqueue that Rob resource for
	// processing. This way, we don't need to implement custom logic for
	// handling Deployment resources. More info on this pattern:
	// https://github.com/kubernetes/community/blob/8cafef897a22026d42f5e5bb3f104febe7e29830/contributors/devel/controllers.md
	deploymentInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.handleObject,
		UpdateFunc: func(old, new interface{}) {
			newDepl := new.(*appsv1.Deployment)
			oldDepl := old.(*appsv1.Deployment)
			if newDepl.ResourceVersion == oldDepl.ResourceVersion {
				// Periodic resync will send update  events for all known Deployments.
				// Two different versions of the same Deployment will always have different RVs.
				return
			}
			controller.handleObject(new)

		},
		DeleteFunc: controller.handleObject,
	})
	return controller
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for workers
// to finish processing their current work items.
func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting Rob controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.deploymentsSynced, c.robsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Info("Starting workers")
	// Launch two workers to process Rob resources
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	klog.Info("Started workers")
	<-stopCh
	klog.Info("Shutting down workers")

	return nil

}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler
func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()
	if shutdown {
		return false
	}
	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		//  do not want this work item being re-queued. For example, we do
		//  not call Forget if a transient error occurs, instead the item is
		//  put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the
		// Rob resource to be synced.
		if err := c.syncHandler(key); err != nil {
			// Put the item back on the workqueue to handle any transient errors.
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		klog.Info("Successfully synced '%s'", key)
		return nil

	}(obj)
	if err != nil {
		utilruntime.HandleError(err)
		return true
	}
	return true

}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the Rob resource
// with the current status of the resource.
func (c *Controller) syncHandler(key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// Get the Rob resource with this namespace/name
	rob, err := c.robsLister.Robs(namespace).Get(name)
	if err != nil {
		// The Rob resource may no longer exist, in which case we stop
		// processing.
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("rob '%s' in work queue no longer exists", key))
			return nil
		}
		return err
	}

	deploymentName := rob.Spec.DeploymentName
	if deploymentName == "" {
		// We choose to absorb the error here as the worker would requeue the
		// resource otherwise. Instead, the next time the resource is updated
		// the resource will be queued again
		utilruntime.HandleError(fmt.Errorf("%s: deployment name must be specified", key))
		return nil
	}

	// Get the deployment with the name specified in Rob.Spec
	deployment, err := c.deploymentsLister.Deployments(rob.Namespace).Get(deploymentName)
	// if the resource doesn't exist, we'll create it
	if errors.IsNotFound(err) {
		deployment, err = c.kubeclientset.AppsV1().Deployments(rob.Namespace).Create(newDeployment(rob))

	}

	// If an error occurs during Get/Create, we'll requeue the item so we can
	// attempt processing again later. This could have been caused by a
	// temporary network failure, or any other transient reason.
	if err != nil {
		return err
	}

	// if the Deployment is not controller by this Rob resource, we should log
	// a warning to the even recorder and ret
	if !metav1.IsControlledBy(deployment, rob) {
		msg := fmt.Sprintf(MessageResourceExists, deployment.Name)
		c.recorder.Event(rob, corev1.EventTypeWarning, ErrResourceExists, msg)
		return fmt.Errorf(msg)
	}

	// If this number of the replicas on the Rob resource is specified, and the
	// number does not equal the current desired on the Deployment, we
	// should update the Deployment resource.
	if rob.Spec.Replicas != nil && *rob.Spec.Replicas != *deployment.Spec.Replicas {
		klog.V(4).Info("Rob %s replicas: %d, deployment replicas: %d", name, *rob.Spec.Replicas, *deployment.Spec.Replicas)
		deployment, err = c.kubeclientset.AppsV1().Deployments(rob.Namespace).Update(newDeployment(rob))
	}

	// if an error occurs during Update, we'll requeue the item so we can
	// attempt processing again later. This could have been caused by a
	// temporary network failure, or any other transient reason
	if err != nil {
		return err
	}

	// Finally, we update the status block of the Rob resource to reflect the
	// the current state of the world
	err = c.updateRobStatus(rob, deployment)
	if err != nil {
		return err
	}
	c.recorder.Event(rob, corev1.EventTypeNormal, SuccessSynced, MessageResourceSynced)
	return nil

}

func (c *Controller) updateRobStatus(rob *robv1alpha1.Rob, deployment *appsv1.Deployment) error {
	// NEVER modify objects from the store. It's a read-only, local cache.
	// You can DeepCopy() to make a deep copy of original object and modify this copy
	// or create a copy manually for better performance
	robCopy := rob.DeepCopy()
	robCopy.Status.AvailableReplicas = deployment.Status.AvailableReplicas
	// If the CustomResourceSubresources feature gate is not enabled,
	// we must use Update instead of UpdateStatus to update the Status block of the Rob resource.
	// UpdateStatus will not allow changes to the Spec of the resource,
	// which is ideal for ensuring nothing other than resource status has been updated.
	_, err := c.robclientset.RobcontrollerV1alpha1().Robs(rob.Namespace).Update(robCopy)
	return err
}

// enqueueRob takes a Rob resource and converts it into a namespace/name
// string with is then put onto the work queue. This method should *not* be
// passed resources of any type other than Rob.

func (c *Controller) enqueueRob(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.AddRateLimited(key)
}

// handleObject will take any resource implementing metav1.Object and attempt
// to find the Rob resource that 'owns' it. It does this by looking at the
// objects metadata.ownerReferences field for an appropriate OwnerReference.
// It then enqueues that Rob resource to be processed. If the object does not
// have an appropriate OwnerReference, it will simply be skipped.
func (c *Controller) handleObject(obj interface{}) {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return
		}
		klog.V(4).Info("Recovered deleted object '%s' from tombstone", object.GetName())
	}
	klog.V(4).Infof("Processing object: %s", object.GetName())
	if onwerRef := metav1.GetControllerOf(object); onwerRef != nil {
		// if this object is not owned by a Rob, we should not do anything more
		// with it.
		if onwerRef.Kind != "Rob" {
			return
		}

		rob, err := c.robsLister.Robs(object.GetNamespace()).Get(onwerRef.Name)
		if err != nil {
			klog.V(4).Infof("ignoring orphaned object '%s' of rob '%s'", object.GetSelfLink, onwerRef.Name)
			return
		}
		c.enqueueRob(rob)
		return
	}
}

// newDeployment creates a new Deployment for a Rob resource. It also sets
// the appropriate OwnerReferences on the resource so handleObject can discover
// the Rob resource that 'owns' it.
func newDeployment(rob *robv1alpha1.Rob) *appsv1.Deployment {
	labels := map[string]string{
		"app":        "nginx",
		"controller": rob.Name,
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rob.Spec.DeploymentName,
			Namespace: rob.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(rob, schema.GroupVersionKind{
					Group:   robv1alpha1.SchemeGroupVersion.Group,
					Version: robv1alpha1.SchemeGroupVersion.Version,
					Kind:    "Rob",
				}),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: rob.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:latest",
						},
					},
				},
			},
		},
	}
}

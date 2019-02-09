package main

import (
	"flag"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"

	clientset "github.com/robel-yemane/rob-controller/pkg/client/clientset/versioned"
	informers "github.com/robel-yemane/rob-controller/pkg/client/informers/externalversions"
	"github.com/robel-yemane/rob-controller/pkg/signals"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	masterURL  string
	kubeconfig string
)

func main() {
	flag.Parse()

	// set up signals so we handle the first shutdown signal gracefuly
	stopCh := signals.SetupSignalHandler()

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s, err.Error()")
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building kubernetes clientset: %s", err.Error())
	}

	robexampleClient, err := clientset.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building example clientset: %s", err.Error())
	}
	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, time.Second*30)
	robexampleInformerFactory := informers.NewSharedInformerFactory(robexampleClient, time.Second*30)

	controller := NewController(kubeClient, robexampleClient,
		kubeInformerFactory.Apps().V1().Deployments(),
		robexampleInformerFactory.Robcontroller().V1alpha1().Robs())

	// notice that there is no need to run Start methods in a seperate goroutine. (i.e. go kubeInformerFactory.Start(stopCh))
	// Start method is non-blocking and runs all registered informers in a dedicated goroutine.
	kubeInformerFactory.Start(stopCh)
	robexampleInformerFactory.Start(stopCh)

	if err = controller.Run(2, stopCh); err != nil {
		klog.Fatalf("Error running controller: %s", err.Error())
	}
}
func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}

package kubernetes

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/eko/monday/internal/config"
	clientmocks "github.com/eko/monday/internal/tests/mocks/kubernetes/client"
	restmocks "github.com/eko/monday/internal/tests/mocks/kubernetes/rest"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

type RESTClient http.Client

func (r RESTClient) Do(request *http.Request) (*http.Response, error) {
	return &http.Response{}, nil
}

func TestNewForwarder(t *testing.T) {
	// Given
	name := "test-forward"
	context := "context-test"
	namespace := "platform"
	ports := []string{"8080:8080"}
	labels := map[string]string{
		"app": "my-test-app",
	}

	initKubeConfig(t)
	defer os.Remove(defaultKubeConfigPath)

	// When
	forwarder, err := NewForwarder(config.ForwarderKubernetes, name, context, namespace, ports, labels)

	// Then
	assert.IsType(t, new(Forwarder), forwarder)
	assert.Nil(t, err)

	assert.Equal(t, config.ForwarderKubernetes, forwarder.forwardType)
	assert.Equal(t, name, forwarder.name)
	assert.Equal(t, context, forwarder.context)
	assert.Equal(t, namespace, forwarder.namespace)
	assert.Equal(t, ports, forwarder.ports)

	assert.Len(t, forwarder.portForwarders, 0)
	assert.Len(t, forwarder.deployments, 0)
}

func TestGetForwardType(t *testing.T) {
	// Given
	initKubeConfig(t)
	defer os.Remove(defaultKubeConfigPath)

	forwarder, err := NewForwarder(config.ForwarderKubernetesRemote, "test-forward", "context-test", "platform", []string{"8080:8080"}, map[string]string{
		"app": "my-test-app",
	})

	// When
	forwardType := forwarder.GetForwardType()

	// Then
	assert.IsType(t, new(Forwarder), forwarder)
	assert.Nil(t, err)

	assert.Equal(t, config.ForwarderKubernetesRemote, forwardType)
}

func TestGetSelector(t *testing.T) {
	// Given
	initKubeConfig(t)
	defer os.Remove(defaultKubeConfigPath)

	forwarder, err := NewForwarder(config.ForwarderKubernetesRemote, "test-forward", "context-test", "platform", []string{"8080:8080"}, map[string]string{
		"app": "my-test-app",
	})

	// When
	selector := forwarder.getSelector()

	// Then
	assert.IsType(t, new(Forwarder), forwarder)
	assert.Nil(t, err)

	assert.Equal(t, "app=my-test-app", selector)
}

func TestGetReadyChannel(t *testing.T) {
	// Given
	initKubeConfig(t)
	defer os.Remove(defaultKubeConfigPath)

	forwarder, err := NewForwarder(config.ForwarderKubernetesRemote, "test-forward", "context-test", "platform", []string{"8080:8080"}, map[string]string{
		"app": "my-test-app",
	})

	// When
	channel := forwarder.GetReadyChannel()

	// Then
	assert.IsType(t, make(chan struct{}), channel)
	assert.Nil(t, err)
}

func TestGetStopChannel(t *testing.T) {
	// Given
	initKubeConfig(t)
	defer os.Remove(defaultKubeConfigPath)

	forwarder, err := NewForwarder(config.ForwarderKubernetesRemote, "test-forward", "context-test", "platform", []string{"8080:8080"}, map[string]string{
		"app": "my-test-app",
	})

	// When
	channel := forwarder.GetStopChannel()

	// Then
	assert.IsType(t, make(chan struct{}), channel)
	assert.Nil(t, err)
}

func TestForward(t *testing.T) {
	// Given
	initKubeConfig(t)
	defer os.Remove(defaultKubeConfigPath)

	forwarder, err := NewForwarder(config.ForwarderKubernetes, "test-forward", "context-test", "backend", []string{"8080:8080"}, map[string]string{
		"app": "my-test-app",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mock Kubernetes Go client calls for retrieving deployment
	deploymentInterface := &clientmocks.DeploymentInterface{}
	deploymentInterface.On("List", metav1.ListOptions{LabelSelector: "app=my-test-app"}).
		Return(&appsv1.DeploymentList{
			Items: []appsv1.Deployment{},
		})

	appsV1Interface := &clientmocks.AppsV1Interface{}
	appsV1Interface.On("Deployments", "backend").
		Return(deploymentInterface)

	clientSetMock := &clientmocks.Interface{}
	clientSetMock.On("AppsV1").
		Return(appsV1Interface)

	// Mock Kubernetes Go client calls for retrieving pods
	podInterface := &clientmocks.PodInterface{}
	podInterface.On("List", metav1.ListOptions{LabelSelector: "app=my-test-app"}).
		Return(&corev1.PodList{
			Items: []corev1.Pod{
				corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "my-test-app-bd4sk",
					},
				},
			},
		}, nil)

	coreV1Interface := &clientmocks.CoreV1Interface{}
	coreV1Interface.On("Pods", "backend").Return(podInterface)

	clientSetMock.On("CoreV1").Return(coreV1Interface)

	// Mock Kubernetes Rest client
	testServer := httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		res.WriteHeader(http.StatusOK)
		res.Write([]byte("ok, port forward is asked"))
	}))

	url, _ := url.Parse(testServer.URL)
	rateLimiter := flowcontrol.NewTokenBucketRateLimiter(2.0, 1)
	restClient := RESTClient{}
	request := restclient.NewRequest(restClient, "POST", url, "/1.0", restclient.ContentConfig{}, restclient.Serializers{}, &restclient.NoBackoff{}, rateLimiter, time.Duration(10*time.Second))

	restClientMock := &restmocks.Interface{}
	restClientMock.On("Post").Return(request)

	forwarder.clientSet = clientSetMock
	forwarder.restClient = restClientMock

	// When
	err = forwarder.Forward()

	// Then
	assert.Contains(t, err.Error(), "ok, port forward is asked")
}

// Initializes a Kubernetes configuration for test environment
func initKubeConfig(t *testing.T) {
	directoryKubeConfig := "/tmp/.kube"
	defaultKubeConfigPath = directoryKubeConfig + "/config"

	err := os.MkdirAll(directoryKubeConfig, os.ModePerm)
	if err != nil {
		t.Fatal(err)
	}

	file, err := os.Create(defaultKubeConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	dir, _ := os.Getwd()
	configFile := dir + "/../../../internal/tests/forwarder/kubernetes/config"

	from, err := os.OpenFile(configFile, os.O_RDONLY, 0666)
	if err != nil {
		t.Fatal(err)
	}
	defer from.Close()

	_, err = io.Copy(file, from)
	if err != nil {
		t.Fatal(err)
	}
}

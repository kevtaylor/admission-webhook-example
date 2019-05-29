package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/golang/glog"
	"k8s.io/api/admission/v1beta1"
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/kubernetes/pkg/apis/core/v1"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()

	// (https://github.com/kubernetes/kubernetes/issues/57982)
	defaulter = runtime.ObjectDefaulter(runtimeScheme)
)

const (
	admissionWebhookAnnotationMutateKey = "admission-webhook-example.banzaicloud.com/mutate"
)

type WebhookServer struct {
	server *http.Server
}

// Webhook Server parameters
type WhSvrParameters struct {
	port           int    // webhook server port
	certFile       string // path to the x509 certificate for https
	keyFile        string // path to the x509 private key matching `CertFile`
	sidecarCfgFile string // path to sidecar injector configuration file
}

type patchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

func init() {
	_ = corev1.AddToScheme(runtimeScheme)
	_ = admissionregistrationv1beta1.AddToScheme(runtimeScheme)
	// defaulting with webhooks:
	// https://github.com/kubernetes/kubernetes/issues/57982
	_ = v1.AddToScheme(runtimeScheme)
}

func addSecretsVolume(deployment appsv1.Deployment) (patch []patchOperation) {

	volume := corev1.Volume{
		Name: "secrets",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory},
		},
	}

	path := "/spec/template/spec/volumes"
	var value interface{}

	if len(deployment.Spec.Template.Spec.Volumes) != 0 {
		path = path + "/-"
		value = volume
	} else {
		value = []corev1.Volume{volume}
	}

	patch = append(patch, patchOperation{
		Op:    "add",
		Path:  path,
		Value: value,
	})

	return patch
}

func addVolumeMount(deployment appsv1.Deployment) (patch []patchOperation) {

	containers := deployment.Spec.Template.Spec.Containers

	volumeMount := corev1.VolumeMount{
		Name:      "secrets",
		MountPath: "/secrets",
	}

	modifiedContainers := []corev1.Container{}

	for _, container := range containers {
		container.VolumeMounts = appendVolumeMountIfMissing(container.VolumeMounts, volumeMount)
		modifiedContainers = append(modifiedContainers, container)
	}

	patch = append(patch, patchOperation{
		Op:    "replace",
		Path:  "/spec/template/spec/containers",
		Value: modifiedContainers,
	})

	return patch
}

func appendVolumeMountIfMissing(slice []corev1.VolumeMount, v corev1.VolumeMount) []corev1.VolumeMount {
	for _, ele := range slice {
		if ele == v {
			return slice
		}
	}
	return append(slice, v)
}

func initContainers(deployment appsv1.Deployment) (patch []patchOperation) {
	initContainers := []corev1.Container{}

	initContainer := corev1.Container{
		Image:   "busybox",
		Name:    "secrets-injector",
		Command: []string{"/bin/sh", "-ec", "echo Hello >/secrets/secret.txt"},
		VolumeMounts: []corev1.VolumeMount{
			corev1.VolumeMount{
				Name:      "secrets",
				MountPath: "/secrets",
			},
		},
	}

	initContainers = append(initContainers, initContainer)

	var initOp string
	if len(deployment.Spec.Template.Spec.InitContainers) != 0 {
		initContainers = append(initContainers, deployment.Spec.Template.Spec.InitContainers...)
		initOp = "replace"
	} else {
		initOp = "add"
	}

	patch = append(patch, patchOperation{
		Op:    initOp,
		Path:  "/spec/template/spec/initContainers",
		Value: initContainers,
	})

	return patch
}

func createPatch(deployment appsv1.Deployment) ([]byte, error) {
	var patch []patchOperation

	patch = append(patch, addSecretsVolume(deployment)...)
	patch = append(patch, initContainers(deployment)...)
	patch = append(patch, addVolumeMount(deployment)...)

	return json.Marshal(patch)
}

// main mutation process
func (whsvr *WebhookServer) mutate(ar *v1beta1.AdmissionReview) *v1beta1.AdmissionResponse {
	req := ar.Request

	glog.Infof("AdmissionReview for Kind=%v, Namespace=%v Name=%v UID=%v patchOperation=%v UserInfo=%v",
		req.Kind, req.Namespace, req.Name, req.UID, req.Operation, req.UserInfo)

	var deployment appsv1.Deployment
	switch req.Kind.Kind {
	case "Deployment":
		if err := json.Unmarshal(req.Object.Raw, &deployment); err != nil {
			glog.Errorf("Could not unmarshal raw object: %v", err)
			return &v1beta1.AdmissionResponse{
				Result: &metav1.Status{
					Message: err.Error(),
				},
			}
		}
		glog.Infof("***** KEVIN ***** Deployment %+v", deployment)

		patchBytes, err := createPatch(deployment)
		if err != nil {
			return &v1beta1.AdmissionResponse{
				Result: &metav1.Status{
					Message: err.Error(),
				},
			}
		}

		glog.Infof("AdmissionResponse: patch=%v\n", string(patchBytes))
		return &v1beta1.AdmissionResponse{
			Allowed: true,
			Patch:   patchBytes,
			PatchType: func() *v1beta1.PatchType {
				pt := v1beta1.PatchTypeJSONPatch
				return &pt
			}(),
		}
	}
	return &v1beta1.AdmissionResponse{
		Allowed: true,
	}
}

// Serve method for webhook server
func (whsvr *WebhookServer) serve(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if r.Body != nil {
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}
	if len(body) == 0 {
		glog.Error("empty body")
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	// verify the content type is accurate
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		glog.Errorf("Content-Type=%s, expect application/json", contentType)
		http.Error(w, "invalid Content-Type, expect `application/json`", http.StatusUnsupportedMediaType)
		return
	}

	var admissionResponse *v1beta1.AdmissionResponse
	ar := v1beta1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &ar); err != nil {
		glog.Errorf("Can't decode body: %v", err)
		admissionResponse = &v1beta1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	} else {
		fmt.Println(r.URL.Path)
		if r.URL.Path == "/mutate" {
			admissionResponse = whsvr.mutate(&ar)
		}
	}

	admissionReview := v1beta1.AdmissionReview{}
	if admissionResponse != nil {
		admissionReview.Response = admissionResponse
		if ar.Request != nil {
			admissionReview.Response.UID = ar.Request.UID
		}
	}

	resp, err := json.Marshal(admissionReview)
	if err != nil {
		glog.Errorf("Can't encode response: %v", err)
		http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
	}
	glog.Infof("Ready to write reponse ...")
	if _, err := w.Write(resp); err != nil {
		glog.Errorf("Can't write response: %v", err)
		http.Error(w, fmt.Sprintf("could not write response: %v", err), http.StatusInternalServerError)
	}
}

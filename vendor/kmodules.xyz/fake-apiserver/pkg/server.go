/*
Copyright AppsCode Inc. and Contributors.

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

package pkg

import (
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	meta_util "kmodules.xyz/client-go/meta"
	rsapi "kmodules.xyz/resource-metadata/apis/meta/v1alpha1"
	"kmodules.xyz/resource-metadata/hub"
	"kmodules.xyz/resource-metadata/hub/resourcedescriptors"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"gomodules.xyz/sets"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apiserver/pkg/endpoints/handlers/negotiation"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

type Options struct {
	// Scheme includes all of the types used by this group and how to convert between them (or
	// to convert objects from outside of this group that are accepted in this API).
	Scheme *runtime.Scheme
	// NegotiatedSerializer controls how this group encodes and decodes data
	NegotiatedSerializer runtime.NegotiatedSerializer
	// ParameterCodec performs conversions for query parameters passed to API calls
	ParameterCodec   runtime.ParameterCodec
	IncludeAPIGroups sets.String
}

type Server struct {
	opts *Options
	reg  *hub.Registry

	m               sync.Mutex
	stores          map[schema.GroupResource]*APIStorage
	resourceVersion int64
	checkedVersion  int64
}

func NewOptions(apigroups ...string) *Options {
	var (
		// Scheme defines methods for serializing and deserializing API objects.
		scheme         = runtime.NewScheme()
		parameterCodec = runtime.NewParameterCodec(scheme)
		// Codecs provides methods for retrieving codecs and serializers for specific
		// versions and content types.
		codecs = serializer.NewCodecFactory(scheme)
	)

	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	metav1.AddToGroupVersion(scheme, metav1.SchemeGroupVersion)

	// TODO: keep the generic API server from wanting this
	unversioned := schema.GroupVersion{Group: "", Version: "v1"}
	scheme.AddUnversionedTypes(unversioned,
		&metav1.Status{},
		&metav1.APIVersions{},
		&metav1.APIGroupList{},
		&metav1.APIGroup{},
		&metav1.APIResourceList{},
	)

	return &Options{
		Scheme:               scheme,
		NegotiatedSerializer: codecs,
		ParameterCodec:       parameterCodec,
		IncludeAPIGroups:     sets.NewString(apigroups...),
	}
}

func NewServer(opts *Options) *Server {
	cache := map[string]*rsapi.ResourceDescriptor{}
	for k, rd := range resourcedescriptors.KnownDescriptors() {
		if meta_util.IsOfficialType(rd.Spec.Resource.Group) ||
			opts.IncludeAPIGroups.Has(rd.Spec.Resource.Group) {
			cache[k] = rd
		}
	}
	return &Server{
		opts:   opts,
		reg:    hub.NewRegistry(hub.KnownUID, hub.NewKVMap(cache)),
		stores: make(map[schema.GroupResource]*APIStorage),
	}
}

func (s *Server) Register(m chi.Router) {
	m.Get("/", s.APIRoot)
	m.Get("/healthz", s.Healthz)
	m.Get("/version", s.Version)
	m.Route("/api", func(m chi.Router) {
		m.Get("/", s.APIVersions)
		m.Get("/v1", s.APIResourceList)
	})
	m.Route("/api/v1/{resource}", func(m chi.Router) {
		m.Post("/", s.Create)
		m.Get("/", s.List)
		m.Delete("/", s.DeleteCollection)
		m.Get("/{name}", s.Get)
		m.Put("/{name}", s.Update)
		m.Put("/{name}/status", s.UpdateStatus)
		m.Patch("/{name}/status", s.PatchStatus)
		m.Patch("/{name}", s.Patch)
		m.Delete("/{name}", s.Delete)
	})
	m.Route("/api/v1/namespaces/{namespace}/{resource}", func(m chi.Router) {
		m.Post("/", s.Create)
		m.Get("/", s.List)
		m.Delete("/", s.DeleteCollection)
		m.Get("/{name}", s.Get)
		m.Put("/{name}", s.Update)
		m.Put("/{name}/status", s.UpdateStatus)
		m.Patch("/{name}/status", s.PatchStatus)
		m.Patch("/{name}", s.Patch)
		m.Delete("/{name}", s.Delete)
	})

	m.Route("/apis", func(m chi.Router) {
		m.Get("/", s.APIGroupList)
		m.Get("/{group}", s.APIGroup)
		m.Get("/{group}/{version}", s.APIResourceList)
	})
	m.Route("/apis/{group}/{version}/{resource}", func(m chi.Router) {
		m.Post("/", s.Create)
		m.Get("/", s.List)
		m.Delete("/", s.DeleteCollection)
		m.Get("/{name}", s.Get)
		m.Put("/{name}", s.Update)
		m.Put("/{name}/status", s.UpdateStatus)
		m.Patch("/{name}/status", s.PatchStatus)
		m.Patch("/{name}", s.Patch)
		m.Delete("/{name}", s.Delete)
	})
	m.Route("/apis/{group}/{version}/namespaces/{namespace}/{resource}", func(m chi.Router) {
		m.Post("/", s.Create)
		m.Get("/", s.List)
		m.Delete("/", s.DeleteCollection)
		m.Get("/{name}", s.Get)
		m.Put("/{name}", s.Update)
		m.Put("/{name}/status", s.UpdateStatus)
		m.Patch("/{name}/status", s.PatchStatus)
		m.Patch("/{name}", s.Patch)
		m.Delete("/{name}", s.Delete)
	})
}

func (s *Server) encoder(w http.ResponseWriter, r *http.Request) runtime.Encoder {
	outputMediaType, _, err := negotiation.NegotiateOutputMediaType(r, OutputSerializer{delegate: s.opts.NegotiatedSerializer}, negotiation.DefaultEndpointRestrictions)
	if err != nil {
		panic(err)
	}
	w.Header().Set("Content-Type", outputMediaType.Accepted.MediaType)
	return outputMediaType.Accepted.Serializer
}

func (s *Server) decoder(r *http.Request) runtime.Decoder {
	info, err := NegotiateInputSerializer(r, false, s.opts.NegotiatedSerializer)
	if err != nil {
		panic(err)
	}
	return info.Serializer
}

func (s *Server) codec(w http.ResponseWriter, r *http.Request) runtime.Codec {
	return runtime.NewCodec(
		s.encoder(w, r),
		s.decoder(r),
	)
}

// NegotiateInputSerializer returns the input serializer for the provided request.
func NegotiateInputSerializer(req *http.Request, streaming bool, ns runtime.NegotiatedSerializer) (runtime.SerializerInfo, error) {
	mediaType := req.Header.Get("Content-Type")
	/*
		const (
			JSONPatchType           PatchType = "application/json-patch+json"
			MergePatchType          PatchType = "application/merge-patch+json"
			StrategicMergePatchType PatchType = "application/strategic-merge-patch+json"
			ApplyPatchType          PatchType = "application/apply-patch+yaml"
		)
	*/
	if strings.HasPrefix(mediaType, "application/") {
		if strings.HasSuffix(mediaType, "+json") {
			mediaType = "application/json"
		} else if strings.HasSuffix(mediaType, "+yaml") {
			mediaType = "application/yaml"
		}
	}
	return negotiation.NegotiateInputSerializerForMediaType(mediaType, streaming, ns)
}

func (s *Server) Store(r *http.Request) *APIStorage {
	gvr := schema.GroupVersionResource{
		Group:    chi.URLParam(r, "group"),
		Version:  chi.URLParam(r, "version"),
		Resource: chi.URLParam(r, "resource"),
	}
	if gvr.Version == "" && gvr.Group == "" {
		gvr.Version = "v1"
	}
	if gvr.Version == "" {
		panic("missing version in URL" + r.URL.String())
	}
	if gvr.Resource == "" {
		panic("missing resource in URL" + r.URL.String())
	}

	return s.StoreForGVR(gvr)
}

func (s *Server) StoreForGVR(gvr schema.GroupVersionResource) *APIStorage {
	s.m.Lock()
	defer s.m.Unlock()

	gvk, _ := s.reg.GVK(gvr)
	namespaced, _ := s.reg.IsGVRNamespaced(gvr)

	store, found := s.stores[gvr.GroupResource()]
	if !found {
		store = &APIStorage{
			s:          s,
			GVR:        gvr,
			GVK:        gvk,
			Namespaced: namespaced,
			Current:    make(map[types.NamespacedName]*unstructured.Unstructured),
			Deleted:    make(map[types.NamespacedName]*unstructured.Unstructured),
		}
		s.stores[gvr.GroupResource()] = store
	}
	return store
}

// https://levelup.gitconnected.com/listening-to-random-available-port-in-go-3541dddbb0c5
// https://medium.com/honestbee-tw-engineer/gracefully-shutdown-in-go-http-server-5f5e6b83da5a
func (s *Server) Run() (*http.Server, *rest.Config, error) {
	m := chi.NewRouter()
	m.Use(middleware.RequestID)
	m.Use(middleware.RealIP)
	m.Use(middleware.Logger)
	m.Use(middleware.Recoverer)
	s.Register(m)

	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, nil, err
	}
	klog.Infoln("listening at", l.Addr().(*net.TCPAddr).Port)
	srv := &http.Server{Handler: m}
	go func() {
		if err := srv.Serve(l); err != nil {
			klog.Errorln(err)
		}
	}()

	cfg := rest.Config{
		Host: fmt.Sprintf("http://127.0.0.1:%d", l.Addr().(*net.TCPAddr).Port),
		ContentConfig: rest.ContentConfig{
			AcceptContentTypes: runtime.ContentTypeJSON,
		},
	}
	return srv, &cfg, nil
}

func (s *Server) Checkpoint() {
	s.m.Lock()
	defer s.m.Unlock()

	s.checkedVersion = s.resourceVersion
}

func (s *Server) NextResourceVersion() int64 {
	s.m.Lock()
	defer s.m.Unlock()

	result := s.resourceVersion
	s.resourceVersion++
	return result
}

func (s *Server) Export() ([]unstructured.Unstructured, []unstructured.Unstructured) {
	s.m.Lock()
	defer s.m.Unlock()

	current := make([]unstructured.Unstructured, 0, len(s.stores))
	deleted := make([]unstructured.Unstructured, 0, len(s.stores))

	for _, store := range s.stores {
		current = append(current, getDirtyObjects(store.Current, s.checkedVersion)...)
		deleted = append(deleted, getDirtyObjects(store.Deleted, s.checkedVersion)...)
	}

	sort.Slice(current, func(i, j int) bool {
		return atoi(current[i].GetResourceVersion()) < atoi(current[j].GetResourceVersion())
	})

	return current, deleted
}

func getDirtyObjects(in map[types.NamespacedName]*unstructured.Unstructured, checkedVersion int64) []unstructured.Unstructured {
	out := make([]unstructured.Unstructured, 0, len(in))
	for _, obj := range in {
		rv, _ := strconv.ParseInt(obj.GetResourceVersion(), 10, 64)
		if rv >= checkedVersion {
			out = append(out, *obj)
		}
	}
	return out
}

func (s *Server) RemoveNamespace(ns string) {
	s.m.Lock()
	defer s.m.Unlock()

	for _, store := range s.stores {
		if store.Namespaced {
			store.RemoveForNamespace(ns)
		}
	}
}

func atoi(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}

type OutputSerializer struct {
	delegate runtime.NegotiatedSerializer
}

var _ runtime.NegotiatedSerializer = &OutputSerializer{}

func (o OutputSerializer) SupportedMediaTypes() []runtime.SerializerInfo {
	a := o.delegate.SupportedMediaTypes()
	b := a[:0]
	for _, x := range a {
		if x.MediaType != runtime.ContentTypeProtobuf {
			b = append(b, x)
		}
	}
	return b
}

func (o OutputSerializer) EncoderForVersion(serializer runtime.Encoder, gv runtime.GroupVersioner) runtime.Encoder {
	return o.delegate.EncoderForVersion(serializer, gv)
}

func (o OutputSerializer) DecoderToVersion(serializer runtime.Decoder, gv runtime.GroupVersioner) runtime.Decoder {
	return o.delegate.DecoderToVersion(serializer, gv)
}

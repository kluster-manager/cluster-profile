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
	"io"
	"net/http"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/go-chi/chi/v5"
	httpw "go.wandrs.dev/http"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/mergepatch"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/validation/field"
	kjson "sigs.k8s.io/json"
)

func (s *Server) Patch(w http.ResponseWriter, r *http.Request) {
	store := s.Store(r)
	codec := s.codec(w, r)

	obj, err := s.PatchImpl(store, codec, r)
	if err != nil {
		_ = codec.Encode(httpw.ErrorToAPIStatus(err), w)
		return
	}
	_ = codec.Encode(obj, w)
}

// https://github.com/kubernetes/kubernetes/blob/21f7bf66fa949dda2b3bec6e3581e248e270e001/staging/src/k8s.io/apiserver/pkg/endpoints/handlers/patch.go#L369
func (s *Server) PatchImpl(store *APIStorage, codec runtime.Codec, r *http.Request) (runtime.Object, error) {
	var opts metav1.PatchOptions
	err := s.opts.ParameterCodec.DecodeParameters(r.URL.Query(), metav1.SchemeGroupVersion, &opts)
	if err != nil {
		return nil, err
	}

	defer r.Body.Close()
	patchBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	key := types.NamespacedName{
		Namespace: chi.URLParam(r, "namespace"),
		Name:      chi.URLParam(r, "name"),
	}
	currentObject, exists := store.Get(key)
	if !exists {
		return nil, apierrors.NewNotFound(store.GVR.GroupResource(), key.String())
	}
	// Encode will convert & return a versioned object in JSON.
	currentObjJS, err := runtime.Encode(codec, currentObject)
	if err != nil {
		return nil, err
	}

	var objToUpdate unstructured.Unstructured

	patchType := types.PatchType(r.Header.Get("Content-Type"))
	err = s.applyJSPatch(codec, store.GVK, patchType, currentObject, &objToUpdate, currentObjJS, patchBytes, opts.FieldValidation)
	if err != nil {
		return nil, err
	}

	if store.Namespaced {
		ns := chi.URLParam(r, "namespace")
		objToUpdate.SetNamespace(ns)
	} else {
		objToUpdate.SetNamespace("")
	}
	store.Insert(&objToUpdate)

	return &objToUpdate, nil
}

func (s *Server) applyJSPatch(codec runtime.Codec, gvk schema.GroupVersionKind, patchType types.PatchType, currentObject, objToUpdate *unstructured.Unstructured, currentObjJS, patchBytes []byte, validationDirective string) error {
	switch patchType {
	case types.JSONPatchType:
		patchObj, err := jsonpatch.DecodePatch(patchBytes)
		if err != nil {
			return apierrors.NewBadRequest(err.Error())
		}
		patchedJS, err := patchObj.Apply(currentObjJS)
		if err != nil {
			return apierrors.NewGenericServerResponse(http.StatusUnprocessableEntity, "", schema.GroupResource{}, "", err.Error(), 0, false)
		}
		_, _, err = codec.Decode(patchedJS, &gvk, objToUpdate)
		return err
	case types.MergePatchType:
		patchedJS, retErr := jsonpatch.MergePatch(currentObjJS, patchBytes)
		if retErr == jsonpatch.ErrBadJSONPatch {
			return apierrors.NewBadRequest(retErr.Error())
		} else if retErr != nil {
			return retErr
		}
		_, _, err := codec.Decode(patchedJS, &gvk, objToUpdate)
		return err
	case types.StrategicMergePatchType:
		schemaReferenceObj, err := s.opts.Scheme.New(gvk)
		if err != nil {
			return err
		}

		originalObjMap := currentObject.UnstructuredContent()
		patchMap := make(map[string]interface{})
		var strictErrs []error
		if validationDirective == metav1.FieldValidationWarn || validationDirective == metav1.FieldValidationStrict {
			strictErrs, err = kjson.UnmarshalStrict(patchBytes, &patchMap)
			if err != nil {
				return apierrors.NewBadRequest(err.Error())
			}
		} else {
			if err = kjson.UnmarshalCaseSensitivePreserveInts(patchBytes, &patchMap); err != nil {
				return apierrors.NewBadRequest(err.Error())
			}
		}
		patchedObjMap, err := strategicpatch.StrategicMergeMapPatch(originalObjMap, patchMap, schemaReferenceObj)
		if err != nil {
			return interpretStrategicMergePatchError(err)
		}

		returnUnknownFields := validationDirective == metav1.FieldValidationWarn || validationDirective == metav1.FieldValidationStrict
		converter := runtime.DefaultUnstructuredConverter
		if err := converter.FromUnstructuredWithValidation(patchedObjMap, objToUpdate, returnUnknownFields); err != nil {
			strictError, isStrictError := runtime.AsStrictDecodingError(err)
			switch {
			case !isStrictError:
				// disregard any sttrictErrs, because it's an incomplete
				// list of strict errors given that we don't know what fields were
				// unknown because StrategicMergeMapPatch failed.
				// Non-strict errors trump in this case.
				return apierrors.NewInvalid(schema.GroupKind{}, "", field.ErrorList{
					field.Invalid(field.NewPath("patch"), fmt.Sprintf("%+v", patchMap), err.Error()),
				})
			// case validationDirective == metav1.FieldValidationWarn:
			//	addStrictDecodingWarnings(requestContext, append(strictErrs, strictError.Errors()...))
			default:
				strictDecodingError := runtime.NewStrictDecodingError(append(strictErrs, strictError.Errors()...))
				return apierrors.NewInvalid(schema.GroupKind{}, "", field.ErrorList{
					field.Invalid(field.NewPath("patch"), fmt.Sprintf("%+v", patchMap), strictDecodingError.Error()),
				})
			}
		} else if len(strictErrs) > 0 {
			switch {
			// case validationDirective == metav1.FieldValidationWarn:
			//	addStrictDecodingWarnings(requestContext, strictErrs)
			default:
				return apierrors.NewInvalid(schema.GroupKind{}, "", field.ErrorList{
					field.Invalid(field.NewPath("patch"), fmt.Sprintf("%+v", patchMap), runtime.NewStrictDecodingError(strictErrs).Error()),
				})
			}
		}
	case types.ApplyPatchType:
	}
	return nil
}

// interpretStrategicMergePatchError interprets the error type and returns an error with appropriate HTTP code.
func interpretStrategicMergePatchError(err error) error {
	switch err {
	case mergepatch.ErrBadJSONDoc, mergepatch.ErrBadPatchFormatForPrimitiveList, mergepatch.ErrBadPatchFormatForRetainKeys, mergepatch.ErrBadPatchFormatForSetElementOrderList, mergepatch.ErrUnsupportedStrategicMergePatchFormat:
		return apierrors.NewBadRequest(err.Error())
	case mergepatch.ErrNoListOfLists, mergepatch.ErrPatchContentNotMatchRetainKeys:
		return apierrors.NewGenericServerResponse(http.StatusUnprocessableEntity, "", schema.GroupResource{}, "", err.Error(), 0, false)
	default:
		return err
	}
}

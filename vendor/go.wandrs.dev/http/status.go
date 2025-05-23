/*
Copyright 2014 The Kubernetes Authors.

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

package http

import (
	"fmt"
	"net/http"
	"reflect"
	"strconv"

	kerr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
)

// statusError is an object that can be converted into an metav1.Status
type statusError interface {
	Status() metav1.Status
}

// ErrorToAPIStatus converts an error to an metav1.Status object.
func ErrorToAPIStatus(err error) *metav1.Status {
	if isNil(err) {
		return &metav1.Status{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Status",
				APIVersion: "v1",
			},
			Status: metav1.StatusSuccess,
			Code:   http.StatusOK,
		}
	}

	switch t := err.(type) {
	case statusError:
		status := t.Status()
		if len(status.Status) == 0 {
			status.Status = metav1.StatusFailure
		}
		switch status.Status {
		case metav1.StatusSuccess:
			if status.Code == 0 {
				status.Code = http.StatusOK
			}
		case metav1.StatusFailure:
			if status.Code == 0 {
				status.Code = http.StatusInternalServerError
			}
		default:
			runtime.HandleError(fmt.Errorf("apiserver received an error with wrong status field : %#+v", err))
			if status.Code == 0 {
				status.Code = http.StatusInternalServerError
			}
		}
		status.Kind = "Status"
		status.APIVersion = "v1"
		// TODO: check for invalid responses
		return &status
	default:
		status := http.StatusInternalServerError
		// Log errors that were not converted to an error status
		// by REST storage - these typically indicate programmer
		// error by not using pkg/api/errors, or unexpected failure
		// cases.
		runtime.HandleError(fmt.Errorf("apiserver received an error that is not an metav1.Status: %#+v: %v", err, err))
		return &metav1.Status{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Status",
				APIVersion: "v1",
			},
			Status:  metav1.StatusFailure,
			Code:    int32(status),
			Reason:  metav1.StatusReasonUnknown,
			Message: err.Error(),
		}
	}
}

func isNil(i any) bool {
	if i == nil {
		return true
	}

	// WARNING: https://stackoverflow.com/a/46275411/244009
	/*for error wrapper interfaces*/
	v := reflect.ValueOf(i)
	// Check if the kind is something that can be nil
	switch v.Kind() {
	case reflect.Ptr, reflect.Slice, reflect.Map, reflect.Chan, reflect.Interface:
		return v.IsNil()
	default:
		return false
	}
}

// ErrorNegotiated renders an error to the response. Returns the HTTP status code of the error.
// The context is optional and may be nil.
func (w *response) APIError(err error) int {
	status := ErrorToAPIStatus(err)
	code := int(status.Code)
	// when writing an error, check to see if the status indicates a retry after period
	if status.Details != nil && status.Details.RetryAfterSeconds > 0 {
		delay := strconv.Itoa(int(status.Details.RetryAfterSeconds))
		w.Header().Set("Retry-After", delay)
	}

	if code == http.StatusNoContent {
		w.WriteHeader(code)
		return code
	}

	if w.log.GetSink() != nil {
		w.log.Error(err, string(status.Reason))
	}
	w.JSON(code, status)
	return code
}

func toStatusReason(statusReason metav1.StatusReason, message ...string) metav1.StatusReason {
	if len(message) > 0 {
		statusReason = metav1.StatusReason(message[0])
	}
	return statusReason
}

// NewInternalError returns an error indicating the item is invalid and cannot be processed.
func NewInternalError(err error, message ...string) *kerr.StatusError {
	return &kerr.StatusError{
		ErrStatus: metav1.Status{
			Status: metav1.StatusFailure,
			Code:   http.StatusInternalServerError,
			Reason: toStatusReason(metav1.StatusReasonInternalError, message...),
			Details: &metav1.StatusDetails{
				Causes: []metav1.StatusCause{{Message: err.Error()}},
			},
			Message: fmt.Sprintf("Internal error occurred: %v", err),
		},
	}
}

// NewNotFound returns a new error which indicates that the requested resource was not found.
func NewNotFound(err error, message ...string) *kerr.StatusError {
	return &kerr.StatusError{
		ErrStatus: metav1.Status{
			Status: metav1.StatusFailure,
			Code:   http.StatusNotFound,
			Reason: toStatusReason(metav1.StatusReasonNotFound, message...),
			Details: &metav1.StatusDetails{
				Causes: []metav1.StatusCause{{Message: err.Error()}},
			},
			Message: fmt.Sprintf("Resource not found: %v", err),
		},
	}
}

// NewAlreadyExists returns an error indicating the item requested already exists.
func NewAlreadyExists(err error, message ...string) *kerr.StatusError {
	return &kerr.StatusError{
		ErrStatus: metav1.Status{
			Status: metav1.StatusFailure,
			Code:   http.StatusConflict,
			Reason: toStatusReason(metav1.StatusReasonAlreadyExists, message...),
			Details: &metav1.StatusDetails{
				Causes: []metav1.StatusCause{{Message: err.Error()}},
			},
			Message: fmt.Sprintf("Resource already exists: %v", err),
		},
	}
}

// NewUnauthorized returns an error indicating the client is not authorized to perform the requested
// action.
func NewUnauthorized(reason string) *kerr.StatusError {
	message := reason
	if len(message) == 0 {
		message = "not authorized"
	}
	return &kerr.StatusError{
		ErrStatus: metav1.Status{
			Status:  metav1.StatusFailure,
			Code:    http.StatusUnauthorized,
			Reason:  metav1.StatusReasonUnauthorized,
			Message: message,
		},
	}
}

// NewForbidden returns an error indicating the requested action was forbidden
func NewForbidden(reason string) *kerr.StatusError {
	message := reason
	if len(message) == 0 {
		message = "not allowed"
	}
	return &kerr.StatusError{
		ErrStatus: metav1.Status{
			Status:  metav1.StatusFailure,
			Code:    http.StatusForbidden,
			Reason:  metav1.StatusReasonForbidden,
			Message: message,
		},
	}
}

// NewConflict returns an error indicating the item can't be updated as provided.
func NewConflict(err error, message ...string) *kerr.StatusError {
	return &kerr.StatusError{
		ErrStatus: metav1.Status{
			Status: metav1.StatusFailure,
			Code:   http.StatusConflict,
			Reason: toStatusReason(metav1.StatusReasonConflict, message...),
			Details: &metav1.StatusDetails{
				Causes: []metav1.StatusCause{{Message: err.Error()}},
			},
			Message: fmt.Sprintf("Operation cannot be fulfilled: %v", err),
		},
	}
}

// NewResourceExpired creates an error that indicates that the requested resource content has expired
func NewResourceExpired(message string) *kerr.StatusError {
	return &kerr.StatusError{
		ErrStatus: metav1.Status{
			Status:  metav1.StatusFailure,
			Code:    http.StatusGone,
			Reason:  metav1.StatusReasonExpired,
			Message: message,
		},
	}
}

// NewBadRequest creates an error that indicates that the request is invalid and can not be processed.
func NewBadRequest(err error, message ...string) *kerr.StatusError {
	return &kerr.StatusError{
		ErrStatus: metav1.Status{
			Status: metav1.StatusFailure,
			Code:   http.StatusBadRequest,
			Reason: toStatusReason(metav1.StatusReasonBadRequest, message...),
			Details: &metav1.StatusDetails{
				Causes: []metav1.StatusCause{{Message: err.Error()}},
			},
			Message: fmt.Sprintf("Operation cannot be fulfilled: %v", err),
		},
	}
}

// NewMethodNotSupported returns an error indicating the requested action is not supported.
func NewMethodNotSupported(err error, message ...string) *kerr.StatusError {
	return &kerr.StatusError{
		ErrStatus: metav1.Status{
			Status: metav1.StatusFailure,
			Code:   http.StatusMethodNotAllowed,
			Reason: toStatusReason(metav1.StatusReasonMethodNotAllowed, message...),
			Details: &metav1.StatusDetails{
				Causes: []metav1.StatusCause{{Message: err.Error()}},
			},
			Message: fmt.Sprintf("Method not supported: %v", err),
		},
	}
}

// NewStatusError returns a generic status type error
func NewStatusError(status int, err error, message ...string) *kerr.StatusError {
	return &kerr.StatusError{
		ErrStatus: metav1.Status{
			Status: metav1.StatusFailure,
			Code:   int32(status),
			Reason: toStatusReason(metav1.StatusReason(http.StatusText(status)), message...),
			Details: &metav1.StatusDetails{
				Causes: []metav1.StatusCause{{Message: err.Error()}},
			},
			Message: err.Error(),
		},
	}
}

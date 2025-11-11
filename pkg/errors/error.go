package errors

import (
	"fmt"
	"net/http"
)

const (
	// Prefix used for error code strings
	// Example:
	//   ErrorCodePrefix = "hyperfleet-adapter"
	//   results in: hyperfleet-adapter-1
	ErrorCodePrefix = "hyperfleet-adapter"

	// HREF for API errors
	ErrorHref = "/api/hyperfleet-adapter/v1/errors/"

	// NotFound occurs when a record is not found in the database
	ErrorNotFound ServiceErrorCode = 1

	// Validation occurs when an object fails validation
	ErrorValidation ServiceErrorCode = 2

	// Conflict occurs when a database constraint is violated
	ErrorConflict ServiceErrorCode = 3

	// Forbidden occurs when a user has been blacklisted
	ErrorForbidden ServiceErrorCode = 4

	// Unauthorized occurs when the requester is not authorized to perform the specified action
	ErrorUnauthorized ServiceErrorCode = 5

	// Unauthenticated occurs when the provided credentials cannot be validated
	ErrorUnauthenticated ServiceErrorCode = 6

	// BadRequest occurs when the request is malformed or invalid
	ErrorBadRequest ServiceErrorCode = 7

	// MalformedRequest occurs when the request body cannot be read
	ErrorMalformedRequest ServiceErrorCode = 8

	// NotImplemented occurs when an API REST method is not implemented in a handler
	ErrorNotImplemented ServiceErrorCode = 9

	// General occurs when an error fails to match any other error code
	ErrorGeneral ServiceErrorCode = 10

	// AdapterConfigNotFound occurs when adapter configuration is not found
	ErrorAdapterConfigNotFound ServiceErrorCode = 11

	// BrokerConnectionError occurs when there's an error connecting to the message broker
	ErrorBrokerConnectionError ServiceErrorCode = 12

	// KubernetesError occurs when there's an error interacting with Kubernetes API
	ErrorKubernetesError ServiceErrorCode = 13

	// HyperFleetAPIError occurs when there's an error calling HyperFleet API
	ErrorHyperFleetAPIError ServiceErrorCode = 14

	// InvalidCloudEvent occurs when a CloudEvent is invalid or malformed
	ErrorInvalidCloudEvent ServiceErrorCode = 15
)

type ServiceErrorCode int

type ServiceErrors []ServiceError

func Find(code ServiceErrorCode) (bool, *ServiceError) {
	for _, err := range Errors() {
		if err.Code == code {
			return true, &err
		}
	}
	return false, nil
}

func Errors() ServiceErrors {
	return ServiceErrors{
		ServiceError{ErrorNotFound, "Resource not found", http.StatusNotFound},
		ServiceError{ErrorValidation, "General validation failure", http.StatusBadRequest},
		ServiceError{ErrorConflict, "An entity with the specified unique values already exists", http.StatusConflict},
		ServiceError{ErrorForbidden, "Forbidden to perform this action", http.StatusForbidden},
		ServiceError{ErrorUnauthorized, "Account is unauthorized to perform this action", http.StatusForbidden},
		ServiceError{ErrorUnauthenticated, "Account authentication could not be verified", http.StatusUnauthorized},
		ServiceError{ErrorBadRequest, "Bad request", http.StatusBadRequest},
		ServiceError{ErrorMalformedRequest, "Unable to read request body", http.StatusBadRequest},
		ServiceError{ErrorNotImplemented, "HTTP Method not implemented for this endpoint", http.StatusMethodNotAllowed},
		ServiceError{ErrorGeneral, "Unspecified error", http.StatusInternalServerError},
		ServiceError{ErrorAdapterConfigNotFound, "Adapter configuration not found", http.StatusNotFound},
		ServiceError{ErrorBrokerConnectionError, "Failed to connect to message broker", http.StatusInternalServerError},
		ServiceError{ErrorKubernetesError, "Kubernetes API error", http.StatusInternalServerError},
		ServiceError{ErrorHyperFleetAPIError, "HyperFleet API error", http.StatusInternalServerError},
		ServiceError{ErrorInvalidCloudEvent, "Invalid CloudEvent", http.StatusBadRequest},
	}
}

type ServiceError struct {
	// Code is the numeric and distinct ID for the error
	Code ServiceErrorCode
	// Reason is the context-specific reason the error was generated
	Reason string
	// HttpCode is the HttpCode associated with the error when the error is returned as an API response
	HttpCode int
}

// New Reason can be a string with format verbs, which will be replaced by the specified values
func New(code ServiceErrorCode, reason string, values ...interface{}) *ServiceError {
	// If the code isn't defined, use the general error code
	var err *ServiceError
	exists, err := Find(code)
	if !exists {
		// Log undefined error code - using fmt.Printf as fallback since we don't have logger here
		fmt.Printf("Undefined error code used: %d\n", code)
		err = &ServiceError{ErrorGeneral, "Unspecified error", http.StatusInternalServerError}
	}

	// If the reason is specified, use it (with formatting)
	if reason != "" {
		err.Reason = fmt.Sprintf(reason, values...)
	}

	return err
}

func (e *ServiceError) Error() string {
	return fmt.Sprintf("%s: %s", *CodeStr(e.Code), e.Reason)
}

func (e *ServiceError) AsError() error {
	return fmt.Errorf("%s", e.Error())
}

func (e *ServiceError) Is404() bool {
	return e.Code == NotFound("").Code
}

func (e *ServiceError) IsConflict() bool {
	return e.Code == Conflict("").Code
}

func (e *ServiceError) IsForbidden() bool {
	return e.Code == Forbidden("").Code
}

func CodeStr(code ServiceErrorCode) *string {
	str := fmt.Sprintf("%s-%d", ErrorCodePrefix, code)
	return &str
}

func Href(code ServiceErrorCode) *string {
	str := fmt.Sprintf("%s%d", ErrorHref, code)
	return &str
}

func NotFound(reason string, values ...interface{}) *ServiceError {
	return New(ErrorNotFound, reason, values...)
}

func GeneralError(reason string, values ...interface{}) *ServiceError {
	return New(ErrorGeneral, reason, values...)
}

func Unauthorized(reason string, values ...interface{}) *ServiceError {
	return New(ErrorUnauthorized, reason, values...)
}

func Unauthenticated(reason string, values ...interface{}) *ServiceError {
	return New(ErrorUnauthenticated, reason, values...)
}

func Forbidden(reason string, values ...interface{}) *ServiceError {
	return New(ErrorForbidden, reason, values...)
}

func NotImplemented(reason string, values ...interface{}) *ServiceError {
	return New(ErrorNotImplemented, reason, values...)
}

func Conflict(reason string, values ...interface{}) *ServiceError {
	return New(ErrorConflict, reason, values...)
}

func Validation(reason string, values ...interface{}) *ServiceError {
	return New(ErrorValidation, reason, values...)
}

func MalformedRequest(reason string, values ...interface{}) *ServiceError {
	return New(ErrorMalformedRequest, reason, values...)
}

func BadRequest(reason string, values ...interface{}) *ServiceError {
	return New(ErrorBadRequest, reason, values...)
}

func AdapterConfigNotFound(reason string, values ...interface{}) *ServiceError {
	return New(ErrorAdapterConfigNotFound, reason, values...)
}

func BrokerConnectionError(reason string, values ...interface{}) *ServiceError {
	return New(ErrorBrokerConnectionError, reason, values...)
}

func KubernetesError(reason string, values ...interface{}) *ServiceError {
	return New(ErrorKubernetesError, reason, values...)
}

func HyperFleetAPIError(reason string, values ...interface{}) *ServiceError {
	return New(ErrorHyperFleetAPIError, reason, values...)
}

func InvalidCloudEvent(reason string, values ...interface{}) *ServiceError {
	return New(ErrorInvalidCloudEvent, reason, values...)
}

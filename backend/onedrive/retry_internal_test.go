package onedrive

import (
	"context"
	"net/http"
	"testing"

	"github.com/rclone/rclone/backend/onedrive/api"
	"github.com/stretchr/testify/assert"
)

func oneDriveAPIError(code, message, innerCode string) *api.Error {
	err := &api.Error{}
	err.ErrorInfo.Code = code
	err.ErrorInfo.Message = message
	err.ErrorInfo.InnerError.Code = innerCode
	return err
}

func TestShouldRetryMalformedDriveIDInvalidRequest(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusBadRequest}
	err := oneDriveAPIError(
		"invalidRequest",
		"The provided drive id appears to be malformed, or does not represent a valid drive.",
		"",
	)

	retry, gotErr := shouldRetry(context.Background(), resp, err)

	assert.True(t, retry)
	assert.Same(t, err, gotErr)
}

func TestShouldRetryMalformedDriveIDMessageCaseInsensitive(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusBadRequest}
	err := oneDriveAPIError(
		"invalidRequest",
		"THE PROVIDED DRIVE ID APPEARS TO BE MALFORMED, OR DOES NOT REPRESENT A VALID DRIVE.",
		"",
	)

	retry, gotErr := shouldRetry(context.Background(), resp, err)

	assert.True(t, retry)
	assert.Same(t, err, gotErr)
}

func TestShouldRetryOtherInvalidRequestRemainsNonRetryable(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusBadRequest}
	err := oneDriveAPIError("invalidRequest", "Some other invalid request.", "")

	retry, gotErr := shouldRetry(context.Background(), resp, err)

	assert.False(t, retry)
	assert.Same(t, err, gotErr)
}

func TestShouldRetryMalformedDriveMessageWithOtherCodeRemainsNonRetryable(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusBadRequest}
	err := oneDriveAPIError(
		"badRequest",
		"The provided drive id appears to be malformed, or does not represent a valid drive.",
		"",
	)

	retry, gotErr := shouldRetry(context.Background(), resp, err)

	assert.False(t, retry)
	assert.Same(t, err, gotErr)
}

func TestShouldRetryPathIsTooLongStillNoRetry(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusBadRequest}
	err := oneDriveAPIError("invalidRequest", "The path is too long.", "pathIsTooLong")

	retry, gotErr := shouldRetry(context.Background(), resp, err)

	assert.False(t, retry)
	assert.Error(t, gotErr)
}

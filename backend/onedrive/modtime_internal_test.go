package onedrive

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rclone/rclone/backend/onedrive/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/pacer"
	"github.com/rclone/rclone/lib/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type modTimeConflictServer struct {
	t                  *testing.T
	cTag               string
	modTime            time.Time
	applyOnConflict    bool
	changeOnConflict   bool
	patchAttempts      int
	getAttempts        int
	patchPaths         []string
	requestedModTime   time.Time
	resourceModifiedOn bool
}

func (s *modTimeConflictServer) item() *api.Item {
	return &api.Item{
		ID:   "fake-id",
		CTag: s.cTag,
		Size: 123,
		FileSystemInfo: &api.FileSystemInfoFacet{
			CreatedDateTime:      api.Timestamp(s.modTime.Add(-time.Hour)),
			LastModifiedDateTime: api.Timestamp(s.modTime),
		},
	}
}

func (s *modTimeConflictServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodPatch:
		s.patchAttempts++
		s.patchPaths = append(s.patchPaths, r.URL.Path)
		var update api.SetFileSystemInfo
		require.NoError(s.t, json.NewDecoder(r.Body).Decode(&update))
		s.requestedModTime = time.Time(update.FileSystemInfo.LastModifiedDateTime)
		if s.resourceModifiedOn && s.patchAttempts == 1 {
			if s.applyOnConflict {
				s.modTime = s.requestedModTime
			}
			if s.changeOnConflict {
				s.cTag = "changed-content"
			}
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"resourceModified","message":"The resource has changed since the caller last read it; usually an eTag mismatch"}}`))
			return
		}
		s.modTime = s.requestedModTime
		require.NoError(s.t, json.NewEncoder(w).Encode(s.item()))
	case http.MethodGet:
		s.getAttempts++
		require.NoError(s.t, json.NewEncoder(w).Encode(s.item()))
	default:
		s.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func newModTimeConflictObject(t *testing.T, s *modTimeConflictServer) *Object {
	t.Helper()
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)

	ctx, ci := fs.AddConfig(context.Background())
	ci.LowLevelRetries = 3
	client := rest.NewClient(server.Client()).SetRoot(server.URL)
	client.SetErrorHandler(errorHandler)
	f := &Fs{
		ci:        ci,
		srv:       client,
		pacer:     fs.NewPacer(ctx, pacer.NewDefault(pacer.MinSleep(time.Millisecond), pacer.MaxSleep(2*time.Millisecond))),
		driveType: driveTypePersonal,
	}
	o := &Object{fs: f, remote: "remote"}
	require.NoError(t, o.setMetaData(s.item()))
	return o
}

func TestSetModTimeRetriesResourceModifiedWithSameContent(t *testing.T) {
	oldTime := time.Date(2026, time.September, 28, 17, 31, 52, 0, time.UTC)
	wantTime := time.Date(2026, time.September, 28, 17, 12, 4, 0, time.UTC)
	s := &modTimeConflictServer{
		t:                  t,
		cTag:               "same-content",
		modTime:            oldTime,
		resourceModifiedOn: true,
	}
	o := newModTimeConflictObject(t, s)

	info, err := o.setModTime(context.Background(), wantTime)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, 2, s.patchAttempts)
	assert.Equal(t, 1, s.getAttempts)
	assert.Equal(t, []string{"/items/fake-id", "/items/fake-id"}, s.patchPaths)
	assert.WithinDuration(t, wantTime, time.Time(info.FileSystemInfo.LastModifiedDateTime), o.fs.Precision())
}

func TestSetModTimeAcceptsResourceModifiedWhenTimestampAlreadyApplied(t *testing.T) {
	oldTime := time.Date(2026, time.September, 28, 17, 31, 52, 0, time.UTC)
	wantTime := time.Date(2026, time.September, 28, 17, 12, 4, 0, time.UTC)
	s := &modTimeConflictServer{
		t:                  t,
		cTag:               "same-content",
		modTime:            oldTime,
		applyOnConflict:    true,
		resourceModifiedOn: true,
	}
	o := newModTimeConflictObject(t, s)

	info, err := o.setModTime(context.Background(), wantTime)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, 1, s.patchAttempts)
	assert.Equal(t, 1, s.getAttempts)
	assert.Equal(t, []string{"/items/fake-id"}, s.patchPaths)
	assert.WithinDuration(t, wantTime, time.Time(info.FileSystemInfo.LastModifiedDateTime), o.fs.Precision())
}

func TestSetModTimeDoesNotRetryResourceModifiedAfterContentChange(t *testing.T) {
	oldTime := time.Date(2026, time.September, 28, 17, 31, 52, 0, time.UTC)
	wantTime := time.Date(2026, time.September, 28, 17, 12, 4, 0, time.UTC)
	s := &modTimeConflictServer{
		t:                  t,
		cTag:               "original-content",
		modTime:            oldTime,
		changeOnConflict:   true,
		resourceModifiedOn: true,
	}
	o := newModTimeConflictObject(t, s)

	info, err := o.setModTime(context.Background(), wantTime)
	require.Error(t, err)
	assert.Nil(t, info)
	var apiErr *api.Error
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, "resourceModified", apiErr.ErrorInfo.Code)
	assert.Equal(t, 1, s.patchAttempts)
	assert.Equal(t, 1, s.getAttempts)
}

func TestSetModTimeDoesNotRetryResourceModifiedWithoutContentTag(t *testing.T) {
	oldTime := time.Date(2026, time.September, 28, 17, 31, 52, 0, time.UTC)
	wantTime := time.Date(2026, time.September, 28, 17, 12, 4, 0, time.UTC)
	s := &modTimeConflictServer{
		t:                  t,
		modTime:            oldTime,
		resourceModifiedOn: true,
	}
	o := newModTimeConflictObject(t, s)

	info, err := o.setModTime(context.Background(), wantTime)
	require.Error(t, err)
	assert.Nil(t, info)
	assert.Equal(t, 1, s.patchAttempts)
	assert.Equal(t, 0, s.getAttempts)
}

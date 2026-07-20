/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestConfigGroupsListReturnsIndependentETagAndMissingMembers(t *testing.T) {
	view := groupCollectionFixture()
	api := &groupAPIStub{view: view}
	recorder := serveConfigGET(t, "/api/v1/config/groups?workspace_id=0123456789abcdef0123456789abcdef", nil, api)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("ETag") != view.ETag || api.workspaceID == nil || *api.workspaceID != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("ETag/workspace = %q/%v", recorder.Header().Get("ETag"), api.workspaceID)
	}
}

func TestConfigGroupCreateForwardsMembersAndStrongETag(t *testing.T) {
	view := groupCollectionFixture()
	api := &groupAPIStub{view: view}
	body := `{"name":"sites","sort_order":2,"members":["conf.d/b.conf","conf.d/a.conf"]}`
	recorder := serveConfigMutation(t, http.MethodPost, "/api/v1/config/groups", body, view.ETag, nil, api)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", recorder.Code, recorder.Body.String())
	}
	if api.createInput.IfMatch != view.ETag || api.createInput.Name != "sites" || len(api.createInput.Members) != 2 {
		t.Fatalf("input = %#v", api.createInput)
	}
}

func groupCollectionFixture() config.GroupCollectionView {
	now := time.Date(2026, time.July, 16, 9, 0, 0, 0, time.UTC)
	return config.GroupCollectionView{
		ETag: `"groups-v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`,
		Groups: []config.GroupView{{
			Group:   config.Group{ID: "0123456789abcdef0123456789abcdef", Name: "sites", SortOrder: 2, Members: []config.RelativePath{"conf.d/a.conf"}, CreatedBy: 7, CreatedAt: now, UpdatedAt: now},
			Missing: []config.RelativePath{"conf.d/a.conf"},
		}},
	}
}

type groupAPIStub struct {
	view        config.GroupCollectionView
	err         error
	workspaceID *config.WorkspaceID
	createInput config.CreateGroupInput
}

func (s *groupAPIStub) ListGroups(_ context.Context, workspaceID *config.WorkspaceID) (config.GroupCollectionView, error) {
	s.workspaceID = workspaceID
	return s.view, s.err
}
func (s *groupAPIStub) CreateGroup(_ context.Context, _ config.Actor, input config.CreateGroupInput) (config.GroupCollectionView, error) {
	s.createInput = input
	return s.view, s.err
}
func (s *groupAPIStub) ReplaceGroup(context.Context, config.Actor, config.GroupID, config.ReplaceGroupInput) (config.GroupCollectionView, error) {
	return s.view, s.err
}
func (s *groupAPIStub) DeleteGroup(context.Context, config.Actor, config.GroupID, config.DeleteGroupInput) (config.GroupCollectionView, error) {
	return s.view, s.err
}

var _ GroupAPI = (*groupAPIStub)(nil)

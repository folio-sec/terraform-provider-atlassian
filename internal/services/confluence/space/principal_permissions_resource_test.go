package space

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

type fakePrincipalPermissionsService struct {
	principal Principal
	current   map[string]PermissionAssignment
	other     []PermissionAssignment
	actions   []string
	nextID    int
	addErr    error
	readErr   error
}

func (f *fakePrincipalPermissionsService) GetSpaceByID(context.Context, string) (Space, error) {
	return Space{ID: "1", Key: "S"}, nil
}

func (f *fakePrincipalPermissionsService) GetSpaceRoleAssignments(context.Context, string, *Principal) ([]SpaceRoleAssignment, error) {
	return nil, nil
}

func (f *fakePrincipalPermissionsService) GetSpacePermissionsAssignments(context.Context, string) ([]PermissionAssignment, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	rows := append([]PermissionAssignment(nil), f.other...)
	for _, row := range f.current {
		rows = append(rows, row)
	}
	return rows, nil
}

func (f *fakePrincipalPermissionsService) AddPermissionToSpace(_ context.Context, _ string, principal Principal, operation PermissionOperation) (string, error) {
	if f.addErr != nil {
		return "", f.addErr
	}
	key := operationKey(operation)
	f.actions = append(f.actions, "add "+key)
	f.put(principal, operation)
	switch key {
	case "read/space":
		f.put(principal, PermissionOperation{Key: "export_content", TargetType: "space"})
	case "create/page":
		f.put(principal, PermissionOperation{Key: "update", TargetType: "page"})
	}
	return f.current[key].ID, nil
}

func TestPrincipalPermissionsDoesNotRecordUnsentMutation(t *testing.T) {
	t.Parallel()
	fake := &fakePrincipalPermissionsService{
		current: map[string]PermissionAssignment{},
		addErr:  fmt.Errorf("authorize: %w", ErrRequestNotSent),
		readErr: errors.New("follow-up read failed"),
	}
	reconciliation := &permissionReconciliation{
		resource:  &principalPermissionsResource{client: fake},
		ctx:       context.Background(),
		spaceKey:  "S",
		principal: Principal{Type: "group", ID: "g"},
		desired: map[string]PermissionOperation{
			"read/space": {Key: "read", TargetType: "space"},
		},
		current: map[string]PermissionAssignment{},
	}
	if err := reconciliation.add("read/space"); err == nil {
		t.Fatal("add() error = nil, want authorization failure")
	}
	if reconciliation.mutationSent {
		t.Fatal("mutationSent = true for a request rejected before dispatch")
	}
}

func (f *fakePrincipalPermissionsService) RemovePermission(_ context.Context, _ string, id string) error {
	for key, row := range f.current {
		if row.ID != id {
			continue
		}
		f.actions = append(f.actions, "remove "+key)
		delete(f.current, key)
		if key == "read/space" {
			f.current = map[string]PermissionAssignment{}
		}
		if key == "create/page" {
			delete(f.current, "update/page")
		}
		if key == "administer/space" {
			delete(f.current, "manage_users/space")
		}
		return nil
	}
	return fmt.Errorf("unknown permission id %s", id)
}

func (f *fakePrincipalPermissionsService) put(principal Principal, operation PermissionOperation) {
	key := operationKey(operation)
	if _, exists := f.current[key]; exists {
		return
	}
	f.nextID++
	f.current[key] = PermissionAssignment{ID: fmt.Sprint(f.nextID), Principal: principal, Operation: operation}
}

func testPermissionsIdentity() principalPermissionsIdentity {
	return principalPermissionsIdentity{SpaceID: types.StringValue("1"), PrincipalType: types.StringValue("group"), PrincipalID: types.StringValue("g")}
}

func TestPrincipalPermissionsReconcileRemovesV1Companions(t *testing.T) {
	t.Parallel()
	fake := &fakePrincipalPermissionsService{principal: Principal{Type: "group", ID: "g"}, current: map[string]PermissionAssignment{}}
	resource := &principalPermissionsResource{client: fake}
	desired := map[string]PermissionOperation{
		"read/space":  {Key: "read", TargetType: "space"},
		"create/page": {Key: "create", TargetType: "page"},
	}
	got, mutationSent, err := resource.reconcile(context.Background(), testPermissionsIdentity(), "S", desired)
	if err != nil {
		t.Fatalf("reconcile() error = %v", err)
	}
	if !mutationSent {
		t.Fatal("reconcile() mutationSent = false, want true")
	}
	if keys := sortedOperationKeys(got); !reflect.DeepEqual(keys, []string{"create/page", "read/space"}) {
		t.Fatalf("final keys = %v", keys)
	}
	wantActions := []string{"add read/space", "add create/page", "remove export_content/space", "remove update/page"}
	if !reflect.DeepEqual(fake.actions, wantActions) {
		t.Fatalf("actions = %v, want %v", fake.actions, wantActions)
	}
}

func TestPrincipalPermissionsDeleteRemovesReadLastAfterCascade(t *testing.T) {
	t.Parallel()
	principal := Principal{Type: "group", ID: "g"}
	fake := &fakePrincipalPermissionsService{principal: principal, current: map[string]PermissionAssignment{}}
	for _, operation := range []PermissionOperation{{Key: "read", TargetType: "space"}, {Key: "administer", TargetType: "space"}, {Key: "delete", TargetType: "page"}, {Key: "manage_users", TargetType: "space"}} {
		fake.put(principal, operation)
	}
	fake.other = []PermissionAssignment{{ID: "other", Principal: Principal{Type: "user", ID: "admin"}, Operation: PermissionOperation{Key: "administer", TargetType: "space"}}}
	resource := &principalPermissionsResource{client: fake}
	if err := resource.deleteAll(context.Background(), testPermissionsIdentity(), "S"); err != nil {
		t.Fatalf("deleteAll() error = %v", err)
	}
	if len(fake.actions) == 0 || fake.actions[len(fake.actions)-1] != "remove read/space" {
		t.Fatalf("actions = %v, want read/space last", fake.actions)
	}
	if len(fake.current) != 0 {
		t.Fatalf("current = %#v, want empty", fake.current)
	}
}

func TestPrincipalPermissionsRefusesLastAdministratorRemoval(t *testing.T) {
	t.Parallel()
	principal := Principal{Type: "group", ID: "g"}
	fake := &fakePrincipalPermissionsService{principal: principal, current: map[string]PermissionAssignment{}}
	fake.put(principal, PermissionOperation{Key: "read", TargetType: "space"})
	fake.put(principal, PermissionOperation{Key: "administer", TargetType: "space"})
	resource := &principalPermissionsResource{client: fake}
	err := resource.deleteAll(context.Background(), testPermissionsIdentity(), "S")
	if err == nil {
		t.Fatal("deleteAll() error = nil, want last-administrator refusal")
	}
	if len(fake.actions) != 0 {
		t.Fatalf("actions = %v, want no mutation before refusal", fake.actions)
	}
}

func TestPermissionMapForMatchesPrincipalTypeAndID(t *testing.T) {
	t.Parallel()
	operation := PermissionOperation{Key: "read", TargetType: "space"}
	rows := []PermissionAssignment{
		{ID: "user-row", Principal: Principal{Type: "user", ID: "shared"}, Operation: operation},
		{ID: "group-row", Principal: Principal{Type: "group", ID: "shared"}, Operation: operation},
	}
	got := permissionMapFor(rows, Principal{Type: "GROUP", ID: "shared"})
	if len(got) != 1 || got["read/space"].ID != "group-row" {
		t.Fatalf("permissionMapFor() = %#v, want only the group row", got)
	}
}

package merchant

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestAccessRejectsNoncanonicalIDsBeforeDatabaseUse(t *testing.T) {
	// A nil pool deliberately proves that bad external IDs never reach SQL.
	access := NewAccess(nil)
	for _, value := range []string{"", "0", "00", "01", "+1", "-1", " 1", "1 ", "1.0", "1e2", "١", "18446744073709551616", "1 OR 1=1"} {
		t.Run(value, func(t *testing.T) {
			ctx := context.Background()
			if _, err := access.MembershipForShop(ctx, value, "1"); !errors.Is(err, ErrInvalidID) {
				t.Fatal("invalid user ID must fail before database access")
			}
			if _, err := access.RequireManager(ctx, "1", value); !errors.Is(err, ErrInvalidID) {
				t.Fatal("invalid shop ID must fail before database access")
			}
			if _, err := access.VisibleShopIDs(ctx, value); !errors.Is(err, ErrInvalidID) {
				t.Fatal("visible range must reject invalid identity")
			}
			if _, err := access.ManagedShopIDs(ctx, value); !errors.Is(err, ErrInvalidID) {
				t.Fatal("managed range must reject invalid identity")
			}
			if _, err := NewService(nil).Workspace(ctx, value); !errors.Is(err, ErrInvalidID) {
				t.Fatal("workspace must reject invalid identity")
			}
		})
	}
	for _, value := range []string{"1", "9007199254740993", "18446744073709551615"} {
		if id, err := parseID(value); err != nil || decimal(id) != value {
			t.Fatal("canonical uint64 IDs must retain all decimal digits")
		}
	}
}

func TestMembershipManagementRoleFailsClosed(t *testing.T) {
	for _, role := range []Role{Owner, Manager, Staff, "", "owner", "ADMIN", "OWNER,STAFF"} {
		if (Membership{Role: role}).IsManager() != (role == Owner || role == Manager) {
			t.Fatal("only exact OWNER and MANAGER roles grant management")
		}
	}
}

func TestWorkspaceJSONRetainsStringIDsAndEmptyArrays(t *testing.T) {
	shopID := uint64(18446744073709551615)
	name, address := "门店", ""
	workspace := assembleWorkspace([]workspaceRow{
		{MerchantID: 9007199254740993, MerchantName: "A", RoleCode: Owner, ShopID: &shopID, ShopName: &name, ShopAddress: &address},
		{MerchantID: 9007199254740994, MerchantName: "B", RoleCode: Staff},
	})
	actual, err := json.Marshal(workspace)
	want := `{"merchants":[{"id":"9007199254740993","name":"A","role":"OWNER","shops":[{"id":"18446744073709551615","name":"门店","address":""}]},{"id":"9007199254740994","name":"B","role":"STAFF","shops":[]}]}`
	if err != nil || string(actual) != want {
		t.Fatal("workspace JSON must keep the exact public contract without membership fields")
	}
	empty, err := json.Marshal(assembleWorkspace(nil))
	if err != nil || string(empty) != `{"merchants":[]}` {
		t.Fatal("a normal user must receive an empty array rather than null")
	}
}

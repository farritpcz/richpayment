package service

import (
	"testing"

	"github.com/farritpcz/richpayment/services/auth/internal/model"
)

// =============================================================================
// TestHasPermission_SuperAdmin verifies that the super_admin role has ALL
// permissions in the system.
//
// The super_admin role is intended for platform operators who need unrestricted
// access. It uses the allPermissions bitmask which has every bit set. This test
// ensures that no permission is accidentally excluded from the super_admin role.
// =============================================================================
func TestHasPermission_SuperAdmin(t *testing.T) {
	mask := DefaultRoles[RoleSuperAdmin]

	// List every defined permission. Super admin must have all of them.
	allPerms := []struct {
		name string
		perm model.Permission
	}{
		{"PermViewMerchants", PermViewMerchants},
		{"PermCreateMerchants", PermCreateMerchants},
		{"PermEditMerchants", PermEditMerchants},
		{"PermViewAgents", PermViewAgents},
		{"PermCreateAgents", PermCreateAgents},
		{"PermEditAgents", PermEditAgents},
		{"PermViewOrders", PermViewOrders},
		{"PermManageOrders", PermManageOrders},
		{"PermApproveWithdrawals", PermApproveWithdrawals},
		{"PermManageBankAccounts", PermManageBankAccounts},
		{"PermViewWallets", PermViewWallets},
		{"PermManageWallets", PermManageWallets},
		{"PermViewAuditLogs", PermViewAuditLogs},
		{"PermManageAdmins", PermManageAdmins},
		{"PermManageRoles", PermManageRoles},
		{"PermEmergencyFreeze", PermEmergencyFreeze},
		{"PermViewReports", PermViewReports},
		{"PermManageSettings", PermManageSettings},
		{"PermManageCommissions", PermManageCommissions},
		{"PermViewPartners", PermViewPartners},
		{"PermManagePartners", PermManagePartners},
	}

	for _, p := range allPerms {
		t.Run(p.name, func(t *testing.T) {
			if !HasPermission(mask, p.perm) {
				t.Errorf("super_admin should have %s, but HasPermission returned false", p.name)
			}
		})
	}
}

// =============================================================================
// TestHasPermission_Viewer verifies that the viewer role has ONLY read/view
// permissions and cannot perform any write or administrative operations.
//
// The viewer role is for read-only dashboard access. Allowing any write
// permission on this role would be a security vulnerability because viewers
// are often external stakeholders (auditors, compliance officers) who should
// not be able to modify data.
// =============================================================================
func TestHasPermission_Viewer(t *testing.T) {
	mask := DefaultRoles[RoleViewer]

	// Permissions the viewer SHOULD have (all read-only).
	shouldHave := []struct {
		name string
		perm model.Permission
	}{
		{"PermViewMerchants", PermViewMerchants},
		{"PermViewAgents", PermViewAgents},
		{"PermViewOrders", PermViewOrders},
		{"PermViewWallets", PermViewWallets},
		{"PermViewAuditLogs", PermViewAuditLogs},
		{"PermViewReports", PermViewReports},
		{"PermViewPartners", PermViewPartners},
	}

	for _, p := range shouldHave {
		t.Run("should_have_"+p.name, func(t *testing.T) {
			if !HasPermission(mask, p.perm) {
				t.Errorf("viewer should have %s, but HasPermission returned false", p.name)
			}
		})
	}

	// Permissions the viewer MUST NOT have (all write/admin operations).
	shouldNotHave := []struct {
		name string
		perm model.Permission
	}{
		{"PermCreateMerchants", PermCreateMerchants},
		{"PermEditMerchants", PermEditMerchants},
		{"PermCreateAgents", PermCreateAgents},
		{"PermEditAgents", PermEditAgents},
		{"PermManageOrders", PermManageOrders},
		{"PermApproveWithdrawals", PermApproveWithdrawals},
		{"PermManageBankAccounts", PermManageBankAccounts},
		{"PermManageWallets", PermManageWallets},
		{"PermManageAdmins", PermManageAdmins},
		{"PermManageRoles", PermManageRoles},
		{"PermEmergencyFreeze", PermEmergencyFreeze},
		{"PermManageSettings", PermManageSettings},
		{"PermManageCommissions", PermManageCommissions},
		{"PermManagePartners", PermManagePartners},
	}

	for _, p := range shouldNotHave {
		t.Run("should_not_have_"+p.name, func(t *testing.T) {
			if HasPermission(mask, p.perm) {
				t.Errorf("viewer should NOT have %s, but HasPermission returned true", p.name)
			}
		})
	}
}

// =============================================================================
// TestHasPermission_Operator verifies that the operator role has the correct
// set of operational permissions but lacks administrative permissions.
//
// Operators handle day-to-day transaction processing (viewing orders, approving
// withdrawals, managing bank accounts) but cannot manage users, change system
// settings, or perform emergency freezes. This separation of duties is a key
// security control.
// =============================================================================
func TestHasPermission_Operator(t *testing.T) {
	mask := DefaultRoles[RoleOperator]

	// Permissions operators SHOULD have.
	shouldHave := []struct {
		name string
		perm model.Permission
	}{
		{"PermViewMerchants", PermViewMerchants},
		{"PermViewAgents", PermViewAgents},
		{"PermViewOrders", PermViewOrders},
		{"PermManageOrders", PermManageOrders},
		{"PermApproveWithdrawals", PermApproveWithdrawals},
		{"PermManageBankAccounts", PermManageBankAccounts},
		{"PermViewWallets", PermViewWallets},
		{"PermViewReports", PermViewReports},
	}

	for _, p := range shouldHave {
		t.Run("should_have_"+p.name, func(t *testing.T) {
			if !HasPermission(mask, p.perm) {
				t.Errorf("operator should have %s, but HasPermission returned false", p.name)
			}
		})
	}

	// Permissions operators MUST NOT have (administrative/dangerous).
	shouldNotHave := []struct {
		name string
		perm model.Permission
	}{
		{"PermCreateMerchants", PermCreateMerchants},
		{"PermEditMerchants", PermEditMerchants},
		{"PermManageAdmins", PermManageAdmins},
		{"PermManageRoles", PermManageRoles},
		{"PermEmergencyFreeze", PermEmergencyFreeze},
		{"PermManageSettings", PermManageSettings},
		{"PermManagePartners", PermManagePartners},
	}

	for _, p := range shouldNotHave {
		t.Run("should_not_have_"+p.name, func(t *testing.T) {
			if HasPermission(mask, p.perm) {
				t.Errorf("operator should NOT have %s, but HasPermission returned true", p.name)
			}
		})
	}
}

// =============================================================================
// TestHasAnyPermission verifies that HasAnyPermission returns true if the role
// mask contains at least ONE of the requested permissions.
//
// This function is used in the authorization middleware when an endpoint
// accepts multiple roles (e.g. "can view OR manage orders"). It should return
// true as soon as any single permission matches, without requiring all of them.
// =============================================================================
func TestHasAnyPermission(t *testing.T) {
	// Viewer has PermViewOrders but not PermManageOrders.
	viewerMask := DefaultRoles[RoleViewer]

	// Subtest: should return true because viewer has PermViewOrders.
	t.Run("has one of the permissions", func(t *testing.T) {
		if !HasAnyPermission(viewerMask, PermViewOrders, PermManageOrders) {
			t.Error("HasAnyPermission should return true when at least one permission matches")
		}
	})

	// Subtest: should return false because viewer has neither permission.
	t.Run("has none of the permissions", func(t *testing.T) {
		if HasAnyPermission(viewerMask, PermManageAdmins, PermEmergencyFreeze) {
			t.Error("HasAnyPermission should return false when no permissions match")
		}
	})

	// Subtest: empty permissions list should return false (vacuous).
	t.Run("empty permissions list", func(t *testing.T) {
		if HasAnyPermission(viewerMask) {
			t.Error("HasAnyPermission with no arguments should return false")
		}
	})
}

// =============================================================================
// TestCombinePermissions verifies that CombinePermissions correctly merges
// multiple permission sets into a single bitmask using bitwise OR.
//
// This function is used when a user has multiple roles and we need to compute
// their effective permission set. The combined mask should contain every
// permission from every input set.
// =============================================================================
func TestCombinePermissions(t *testing.T) {
	// Combine two individual permissions.
	combined := CombinePermissions(PermViewMerchants, PermViewOrders)

	if !HasPermission(combined, PermViewMerchants) {
		t.Error("combined mask should include PermViewMerchants")
	}
	if !HasPermission(combined, PermViewOrders) {
		t.Error("combined mask should include PermViewOrders")
	}
	// A permission that was not in either input should be absent.
	if HasPermission(combined, PermManageAdmins) {
		t.Error("combined mask should NOT include PermManageAdmins")
	}

	// Combining overlapping sets should produce the union.
	operatorPerms := DefaultRoles[RoleOperator]
	financePerms := DefaultRoles[RoleFinance]
	merged := CombinePermissions(operatorPerms, financePerms)

	// The merged set should include permissions from both roles.
	if !HasPermission(merged, PermManageOrders) { // from operator
		t.Error("merged mask should include PermManageOrders from operator")
	}
	if !HasPermission(merged, PermManageCommissions) { // from finance
		t.Error("merged mask should include PermManageCommissions from finance")
	}
}

// =============================================================================
// TestDefaultRoles verifies that every named role is defined in DefaultRoles
// and has a non-zero permission mask.
//
// A missing or zero-permission role would lock out all users assigned to that
// role, which would be a critical configuration bug. This test catches such
// regressions early.
// =============================================================================
func TestDefaultRoles(t *testing.T) {
	roles := []model.Role{
		RoleSuperAdmin,
		RoleAdmin,
		RoleOperator,
		RoleFinance,
		RoleViewer,
	}

	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			mask, ok := DefaultRoles[role]
			if !ok {
				t.Fatalf("role %q is not defined in DefaultRoles", role)
			}
			if mask == 0 {
				t.Errorf("role %q has zero permissions; this would lock out all users with this role", role)
			}
		})
	}

	// Verify that super_admin has strictly more permissions than other roles.
	// This is an important invariant: super_admin should be a superset of
	// every other role's permissions.
	superAdminMask := DefaultRoles[RoleSuperAdmin]
	for _, role := range []model.Role{RoleAdmin, RoleOperator, RoleFinance, RoleViewer} {
		roleMask := DefaultRoles[role]
		// Every bit in roleMask should also be set in superAdminMask.
		if roleMask&superAdminMask != roleMask {
			t.Errorf("super_admin mask (%d) does not contain all permissions from role %q (%d)",
				superAdminMask, role, roleMask)
		}
	}
}

// =============================================================================
// TestRolePermissions verifies the RolePermissions helper function.
//
// RolePermissions is a convenience function that looks up a role's permission
// mask from the DefaultRoles map. It returns 0 for unknown roles, which
// effectively denies all access (fail-closed behavior).
// =============================================================================
func TestRolePermissions(t *testing.T) {
	// Known role should return the correct mask.
	t.Run("known role", func(t *testing.T) {
		mask := RolePermissions(RoleViewer)
		expected := DefaultRoles[RoleViewer]
		if mask != expected {
			t.Errorf("RolePermissions(viewer) = %d, want %d", mask, expected)
		}
	})

	// Unknown role should return 0 (deny all).
	t.Run("unknown role", func(t *testing.T) {
		mask := RolePermissions("nonexistent_role")
		if mask != 0 {
			t.Errorf("RolePermissions(unknown) = %d, want 0", mask)
		}
	})
}

package service

import "github.com/farritpcz/richpayment/services/auth/internal/model"

// Permission constants using bitmask flags.
const (
	PermViewMerchants      model.Permission = 1 << iota // 1
	PermCreateMerchants                                  // 2
	PermEditMerchants                                    // 4
	PermViewAgents                                       // 8
	PermCreateAgents                                     // 16
	PermEditAgents                                       // 32
	PermViewOrders                                       // 64
	PermManageOrders                                     // 128
	PermApproveWithdrawals                               // 256
	PermManageBankAccounts                               // 512
	PermViewWallets                                      // 1024
	PermManageWallets                                    // 2048
	PermViewAuditLogs                                    // 4096
	PermManageAdmins                                     // 8192
	PermManageRoles                                      // 16384
	PermEmergencyFreeze                                  // 32768
	PermViewReports                                      // 65536
	PermManageSettings                                   // 131072
	PermManageCommissions                                // 262144
	PermViewPartners                                     // 524288
	PermManagePartners                                   // 1048576
)

// Named roles.
const (
	RoleSuperAdmin model.Role = "super_admin"
	RoleAdmin      model.Role = "admin"
	RoleOperator   model.Role = "operator"
	RoleFinance    model.Role = "finance"
	RoleViewer     model.Role = "viewer"
)

// allPermissions is a mask with every permission bit set.
const allPermissions model.Permission = (1 << 21) - 1

// DefaultRoles maps each role to its permission bitmask.
var DefaultRoles = map[model.Role]model.Permission{
	RoleSuperAdmin: allPermissions,

	RoleAdmin: PermViewMerchants | PermCreateMerchants | PermEditMerchants |
		PermViewAgents | PermCreateAgents | PermEditAgents |
		PermViewOrders | PermManageOrders |
		PermApproveWithdrawals | PermManageBankAccounts |
		PermViewWallets | PermManageWallets |
		PermViewAuditLogs | PermManageAdmins |
		PermViewReports | PermManageSettings | PermManageCommissions |
		PermViewPartners | PermManagePartners,

	RoleOperator: PermViewMerchants | PermViewAgents |
		PermViewOrders | PermManageOrders |
		PermApproveWithdrawals | PermManageBankAccounts |
		PermViewWallets | PermViewReports,

	RoleFinance: PermViewMerchants | PermViewAgents |
		PermViewOrders | PermApproveWithdrawals |
		PermViewWallets | PermManageWallets |
		PermViewReports | PermManageCommissions,

	RoleViewer: PermViewMerchants | PermViewAgents |
		PermViewOrders | PermViewWallets |
		PermViewAuditLogs | PermViewReports | PermViewPartners,
}

// HasPermission checks whether the given role mask includes the specified permission.
func HasPermission(roleMask model.Permission, perm model.Permission) bool {
	return roleMask&perm == perm
}

// HasAnyPermission checks whether the role mask includes at least one of the given permissions.
func HasAnyPermission(roleMask model.Permission, perms ...model.Permission) bool {
	for _, p := range perms {
		if roleMask&p == p {
			return true
		}
	}
	return false
}

// CombinePermissions merges multiple permission sets into one mask.
func CombinePermissions(perms ...model.Permission) model.Permission {
	var combined model.Permission
	for _, p := range perms {
		combined |= p
	}
	return combined
}

// RolePermissions returns the permission mask for a named role.
// Returns 0 if the role is unknown.
func RolePermissions(role model.Role) model.Permission {
	return DefaultRoles[role]
}

// SPDX-License-Identifier: Apache-2.0













package console

import "context"




type Role string

const (
	RoleViewer        Role = "viewer"
	RoleAuditor       Role = "auditor"
	RoleOperator      Role = "operator"
	RoleSecurityAdmin Role = "security_admin"
	RoleSuperAdmin    Role = "super_admin"
	RoleAgentService  Role = "agent_service"
)


var rank = map[Role]int{
	RoleViewer: 1, RoleAuditor: 2, RoleOperator: 3,
	RoleSecurityAdmin: 4, RoleSuperAdmin: 5,
}




func (r Role) Satisfies(need Role) bool {
	if r == RoleAgentService || need == RoleAgentService {
		return false
	}
	return rank[r] >= rank[need] && rank[r] > 0
}


type Args map[string]any


type Output struct {
	Text string
	Data map[string]any
}


type Handler func(ctx context.Context, a Args) (Output, error)


type Command struct {
	Name string

	Summary string
	MinRole Role


	Mutating bool
	Handler  Handler
}


type Registry interface {
	Register(c Command) error
	Lookup(name string) (Command, bool)
	List() []Command



	Invoke(ctx context.Context, name string, a Args) (Output, error)
}

package rbac

import "github.com/casbin/casbin/v2"

type policyRule struct {
	Subject string
	Domain  string
	Object  string
	Action  string
}

var defaultPolicyRules = []policyRule{
	{Subject: "tenant_owner", Domain: "tenant:*", Object: "/v1/admin/*", Action: "*"},
	{Subject: "tenant_admin", Domain: "tenant:*", Object: "/v1/admin/*", Action: "*"},
	{Subject: "tenant_owner", Domain: "tenant:*", Object: "/api/v1/admin/*", Action: "*"},
	{Subject: "tenant_admin", Domain: "tenant:*", Object: "/api/v1/admin/*", Action: "*"},
}

func SeedDefaultPolicy(enforcer *casbin.Enforcer) (bool, error) {
	changed := false
	for _, rule := range defaultPolicyRules {
		has, err := enforcer.HasPolicy(rule.Subject, rule.Domain, rule.Object, rule.Action)
		if err != nil {
			return false, err
		}
		if has {
			continue
		}
		added, err := enforcer.AddPolicy(rule.Subject, rule.Domain, rule.Object, rule.Action)
		if err != nil {
			return false, err
		}
		if added {
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	if err := enforcer.SavePolicy(); err != nil {
		return false, err
	}
	return true, nil
}

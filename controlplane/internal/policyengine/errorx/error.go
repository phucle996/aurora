package policyErrorx

import "errors"

var (
	ErrPolicyInvalid = errors.New("policy engine: invalid policy")
)

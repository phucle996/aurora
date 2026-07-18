package handler

import (
	"context"
	"fmt"

	billingSvcInterface "cost-manager/api/internal/domain/service"
	authv1 "cost-manager/api/internal/genproto/billing/auth/v1"
	"cost-manager/api/pkg/logger"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

// SubscribeAuth registers NATS subscription on billing.auth.verify_credentials using Protobuf serialization
func SubscribeAuth(nc *nats.Conn, authSvc billingSvcInterface.AuthService) (*nats.Subscription, error) {
	const op = "nats.auth.subscribe"
	if authSvc == nil {
		return nil, fmt.Errorf("%s: authService cannot be nil", op)
	}

	sub, err := nc.QueueSubscribe("billing.auth.verify_credentials", "billing_auth_group", func(msg *nats.Msg) {
		var req authv1.VerifyBillingCredentialsRequest
		if err := proto.Unmarshal(msg.Data, &req); err != nil {
			logger.SysError(op, "unmarshal request payload failed: "+err.Error())
			resp := &authv1.VerifyBillingCredentialsResponse{
				Valid:        false,
				ErrorMessage: "Invalid request payload format",
			}
			if respBytes, err := proto.Marshal(resp); err == nil {
				_ = msg.Respond(respBytes)
			}
			return
		}

		// Dispatch authentication request to domain service layer
		user, err := authSvc.VerifyCredentials(context.Background(), req.GetEmployeeCode(), req.GetSecretKey())

		var resp *authv1.VerifyBillingCredentialsResponse
		if err != nil {
			logger.SysWarn(op, "verify credentials failed: "+err.Error())
			resp = &authv1.VerifyBillingCredentialsResponse{
				Valid:        false,
				ErrorMessage: "Invalid employee code or secret key",
			}
		} else {
			resp = &authv1.VerifyBillingCredentialsResponse{
				Valid:        true,
				UserId:       user.ID.String(),
				EmployeeCode: user.EmployeeCode,
				RoleId:       user.RoleID,
				Level:        int32(user.Level),
			}
		}

		respBytes, err := proto.Marshal(resp)
		if err != nil {
			logger.SysError(op, "marshal response payload failed: "+err.Error())
			return
		}

		_ = msg.Respond(respBytes)
	})

	if err != nil {
		return nil, fmt.Errorf("%s: subscribe to subject failed: %w", op, err)
	}

	logger.SysInfo(op, "Successfully subscribed to billing.auth.verify_credentials protobuf channel")
	return sub, nil
}

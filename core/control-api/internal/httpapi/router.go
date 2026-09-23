// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/batonos/baton/core/control-api/internal/auth"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)










func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()



	mux.HandleFunc("GET /healthz", a.handleHealthz)
	mux.HandleFunc("GET /readyz", a.handleReadyz)
	mux.HandleFunc("POST "+BasePath+"/enroll", a.handleEnroll)





	mux.HandleFunc("POST "+BasePath+"/operators/enroll", a.handleOperatorEnroll)





	mux.HandleFunc("GET "+BasePath+"/network", a.handleNetworkDescriptor)
	mux.HandleFunc("POST "+BasePath+"/join-requests", a.handleJoinRequestCreate)
	mux.HandleFunc("GET "+BasePath+"/join-requests/{request_id}", a.handleJoinRequestGet)
	mux.HandleFunc("POST "+BasePath+"/join-requests/{request_id}/collect", a.handleJoinRequestCollect)

	mux.HandleFunc("GET "+BasePath+"/public/resources", a.handlePublicResourceList)
	mux.HandleFunc("GET "+BasePath+"/public/resources/{id}", a.handlePublicResourceGet)





















	mux.HandleFunc("POST "+BasePath+"/networks/self/access-tokens", a.handleAccessTokenExchange)
	mux.HandleFunc("GET "+BasePath+"/networks/self/access-resources/{id}", a.handleAccessResourceGet)




	mux.HandleFunc("POST "+BasePath+"/networks/self/delivery-tokens", a.handleDeliveryTokenExchange)
	mux.HandleFunc("POST "+BasePath+"/networks/self/deliveries", a.handleDeliveryReceive)




	mux.HandleFunc("POST "+BasePath+"/networks/self/delivery-reports", a.handleDeliveryReportReceive)


	mux.HandleFunc("GET "+BasePath+"/agent/channel", a.handleAgentChannel)
	mux.HandleFunc("POST "+BasePath+"/agent/certificate/renew", a.handleCertRenew)
	mux.HandleFunc("GET "+BasePath+"/ha/snapshot", a.handleSnapshot)


	mux.HandleFunc("POST "+BasePath+"/ha/ca-bundle", a.handleCABundle)


	mux.HandleFunc("GET "+BasePath+"/agent/skills", a.handleAgentSkillList)


	mux.HandleFunc("GET "+BasePath+"/agent/contacts", a.handleAgentContacts)

	mux.HandleFunc("GET "+BasePath+"/agent/network-resources", a.handleAgentResourceList)
	mux.HandleFunc("GET "+BasePath+"/agent/network-resources/{id}", a.handleAgentResourceGet)


	mux.HandleFunc("GET "+BasePath+"/agent/network-resources/{id}/archive", a.handleAgentResourceArchive)








	mux.HandleFunc("GET "+BasePath+"/agent/cloud-resources/{network}/{resource}", a.handleAgentCloudResource)
	mux.HandleFunc("GET "+BasePath+"/agent/archives/{digest}", a.handleAgentArchiveByDigest)

	mux.HandleFunc("POST "+BasePath+"/agent/network-resources", a.handleAgentPublish)
	mux.HandleFunc("GET "+BasePath+"/agent/skills/{skill_id}/archive", a.handleAgentSkillArchive)



	mux.HandleFunc("POST "+BasePath+"/agent/messages/attachments", a.handleAgentAttachmentUpload)
	mux.HandleFunc("GET "+BasePath+"/agent/messages/{message_id}/attachments/{idx}", a.handleAgentAttachmentDownload)



	mux.HandleFunc("GET "+BasePath+"/join-requests", a.handleJoinRequestList)
	mux.HandleFunc("POST "+BasePath+"/join-requests/{agent}/admit", a.handleJoinRequestDecide(spi.JoinAdmitted))
	mux.HandleFunc("POST "+BasePath+"/join-requests/{agent}/deny", a.handleJoinRequestDecide(spi.JoinDenied))
	mux.HandleFunc("POST "+BasePath+"/tokens", a.handleTokenCreate)
	mux.HandleFunc("GET "+BasePath+"/tokens", a.handleTokenList)
	mux.HandleFunc("DELETE "+BasePath+"/tokens/{token_id}", a.handleTokenRevoke)
	mux.HandleFunc("POST "+BasePath+"/operators/invitations", a.handleOperatorInvite)
	mux.HandleFunc("GET "+BasePath+"/operators", a.handleOperatorList)
	mux.HandleFunc("POST "+BasePath+"/operators/{name}/revoke", a.handleOperatorRevoke)

	mux.HandleFunc("GET "+BasePath+"/nodes", a.handleNodeList)
	mux.HandleFunc("GET "+BasePath+"/nodes/{node_id}", a.handleNodeGet)
	mux.HandleFunc("POST "+BasePath+"/nodes/{node_id}/label", a.handleNodeLabel)
	mux.HandleFunc("POST "+BasePath+"/nodes/{node_id}/avatar", a.handleNodeAvatarSet)
	mux.HandleFunc("GET "+BasePath+"/nodes/{node_id}/avatar", a.handleNodeAvatarGet)
	mux.HandleFunc("POST "+BasePath+"/nodes/{node_id}/revoke", a.handleNodeRevoke)


	mux.HandleFunc("POST "+BasePath+"/nodes/{node_id}/suspend", a.handleNodeSuspend(true))
	mux.HandleFunc("POST "+BasePath+"/nodes/{node_id}/resume", a.handleNodeSuspend(false))
	mux.HandleFunc("POST "+BasePath+"/nodes/{node_id}/console", a.handleConsole)

	mux.HandleFunc("GET "+BasePath+"/pty", a.handlePty)




	mux.HandleFunc("GET "+BasePath+"/nodes/{node_id}/takeover", a.handleTakeoverStatus)
	mux.HandleFunc("POST "+BasePath+"/nodes/{node_id}/takeover/acquire", a.handleTakeoverAcquire)
	mux.HandleFunc("POST "+BasePath+"/nodes/{node_id}/takeover/renew", a.handleTakeoverRenew)
	mux.HandleFunc("POST "+BasePath+"/nodes/{node_id}/takeover/release", a.handleTakeoverRelease)



	mux.HandleFunc("POST "+BasePath+"/messages", a.handleMessageSend)
	mux.HandleFunc("GET "+BasePath+"/messages", a.handleMessageList)


	mux.HandleFunc("GET "+BasePath+"/messages/{message_id}", a.handleMessageGet)




	mux.HandleFunc("POST "+BasePath+"/messages/{message_id}/payload", a.handleMessageRead)


	mux.HandleFunc("POST "+BasePath+"/messages/mark-all-read", a.handleMessageMarkAllRead)
	mux.HandleFunc("DELETE "+BasePath+"/messages/{message_id}", a.handleMessageDelete)
	mux.HandleFunc("GET "+BasePath+"/identities", a.handleIdentityList)
	mux.HandleFunc("POST "+BasePath+"/identities", a.handleIdentityBind)


	mux.HandleFunc("GET "+BasePath+"/inbox/policy/{identity}", a.handleInboxPolicyGet)
	mux.HandleFunc("PUT "+BasePath+"/inbox/policy/{identity}", a.handleInboxPolicySet)

	mux.HandleFunc("GET "+BasePath+"/capabilities", a.handleCapabilityList)
	mux.HandleFunc("POST "+BasePath+"/capabilities/{id}/revoke", a.handleCapabilityGrant(false))
	mux.HandleFunc("POST "+BasePath+"/capabilities/{id}/grant", a.handleCapabilityGrant(true))

	mux.HandleFunc("POST "+BasePath+"/calls", a.handleCallCreate)
	mux.HandleFunc("GET "+BasePath+"/calls/{call_id}", a.handleCallGet)




	mux.HandleFunc("GET "+BasePath+"/core/status", a.handleCoreStatus)

	mux.HandleFunc("GET "+BasePath+"/core/actions", a.handleCoreActions)
	mux.HandleFunc("POST "+BasePath+"/core/proposals", a.handleCoreProposal)


	mux.HandleFunc("GET "+BasePath+"/core/transactions", a.handleCoreTransactionsList)
	mux.HandleFunc("GET "+BasePath+"/core/transactions/{id}", a.handleCoreTransactionGet)
	mux.HandleFunc("GET "+BasePath+"/core/transactions/{id}/record", a.handleCoreTransactionRecord)
	mux.HandleFunc("GET "+BasePath+"/core/transactions/{id}/receipt", a.handleCoreTransactionReceipt)
	mux.HandleFunc("GET "+BasePath+"/core/context", a.handleCoreContext)


	mux.HandleFunc("POST "+BasePath+"/core/transactions/{id}/approve", a.handleCoreTransactionDecide(spi.TxApproved))
	mux.HandleFunc("POST "+BasePath+"/core/transactions/{id}/reject", a.handleCoreTransactionDecide(spi.TxRejected))

	mux.HandleFunc("GET "+BasePath+"/events", a.handleEvents)



	mux.HandleFunc("GET "+BasePath+"/events/latest", a.handleEventsLatest)
	mux.HandleFunc("GET "+BasePath+"/system/info", a.handleSystemInfo)


	mux.HandleFunc("POST "+BasePath+"/system/promote", a.handlePromote)

	mux.HandleFunc("POST "+BasePath+"/system/demote", a.handleDemote)


	mux.HandleFunc("POST "+BasePath+"/system/transfer/accept", a.handleTransferAccept)




	mux.HandleFunc("GET "+BasePath+"/networks/self", a.handleNetworkSelf)




	mux.HandleFunc("GET "+BasePath+"/integrations", a.handleIntegrationList)
	mux.HandleFunc("POST "+BasePath+"/integrations", a.handleIntegrationCreate)
	mux.HandleFunc("POST "+BasePath+"/integrations/{id}/enable", a.handleIntegrationSetEnabled(true))
	mux.HandleFunc("POST "+BasePath+"/integrations/{id}/disable", a.handleIntegrationSetEnabled(false))
	mux.HandleFunc("DELETE "+BasePath+"/integrations/{id}", a.handleIntegrationDelete)



	mux.HandleFunc("POST "+BasePath+"/networks/self/attestations", a.handleAttestationCreate)
	mux.HandleFunc("POST "+BasePath+"/networks/self/name", a.handleNetworkRename)


	mux.HandleFunc("POST "+BasePath+"/networks/self/admission", a.handleNetworkAdmission)



	mux.HandleFunc("POST "+BasePath+"/messages/attachments", a.handleAttachmentUpload)
	mux.HandleFunc("GET "+BasePath+"/messages/{message_id}/attachments/{idx}", a.handleAttachmentDownload)
	mux.HandleFunc("POST "+BasePath+"/networks/self/avatar", a.handleNetworkAvatar)
	mux.HandleFunc("POST "+BasePath+"/networks/self/cloud-sign", a.handleNetworkCloudSign)

	mux.HandleFunc("POST "+BasePath+"/networks/self/resources", a.handleResourcePublish)








	mux.HandleFunc("POST "+BasePath+"/networks/self/resource-archives/{digest}", a.handleResourceArchiveUpload)
	mux.HandleFunc("GET "+BasePath+"/networks/self/resources", a.handleResourceListOwner)
	mux.HandleFunc("GET "+BasePath+"/networks/self/resources/{id}", a.handleResourceGetOwner)
	mux.HandleFunc("DELETE "+BasePath+"/networks/self/resources/{id}", a.handleResourceDelete)
	mux.HandleFunc("POST "+BasePath+"/networks/self/resources/{id}/move", a.handleResourceMove)
	mux.HandleFunc("POST "+BasePath+"/networks/self/resources/{id}/tags", a.handleResourceTag)



	mux.HandleFunc("POST "+BasePath+"/networks/self/resources/{id}/admit", a.handleResourceDecide(spi.ReviewAdmitted))
	mux.HandleFunc("POST "+BasePath+"/networks/self/resources/{id}/deny", a.handleResourceDecide(spi.ReviewDenied))
	mux.HandleFunc("POST "+BasePath+"/networks/self/addresses", a.handleNetworkAddressPut)
	mux.HandleFunc("POST "+BasePath+"/networks/self/endpoints", a.handleNetworkEndpointPut)




	mux.HandleFunc("GET "+BasePath+"/skills", a.handleSkillList)
	mux.HandleFunc("POST "+BasePath+"/skills", a.handleSkillCreate)
	mux.HandleFunc("DELETE "+BasePath+"/skills/{skill_id}", a.handleSkillDelete)
	mux.HandleFunc("POST "+BasePath+"/skills/{skill_id}/install", a.handleSkillInstall)
	mux.HandleFunc("POST "+BasePath+"/skills/{skill_id}/uninstall", a.handleSkillUninstall)



	mux.HandleFunc("GET "+BasePath+"/skills/{skill_id}/nodes/{node_id}/code", a.handleSkillNodeCode)
	mux.HandleFunc("PUT "+BasePath+"/skills/{skill_id}/nodes/{node_id}/code", a.handleSkillNodeCode)

	mux.HandleFunc("POST "+BasePath+"/networks/self/invitations", a.handleInvitationCreate)
	mux.HandleFunc("POST "+BasePath+"/networks/self/transfers", a.handleTransferApprove)





	mux.HandleFunc("POST "+BasePath+"/networks/self/access-grants", a.handleAccessGrantIssue)



	mux.HandleFunc("POST "+BasePath+"/networks/self/delivery-grants", a.handleDeliveryGrantIssue)




	mux.HandleFunc("GET "+BasePath+"/networks/self/delivery-grants", a.handleDeliveryGrantList)
	mux.HandleFunc("DELETE "+BasePath+"/networks/self/delivery-grants/{grant_id}", a.handleDeliveryGrantRevoke)

	mux.HandleFunc("POST "+BasePath+"/networks/self/spool-collect", a.handleSpoolCollect)
	mux.HandleFunc("GET "+BasePath+"/networks/self/access-grants", a.handleAccessGrantList)
	mux.HandleFunc("DELETE "+BasePath+"/networks/self/access-grants/{grant_id}", a.handleAccessGrantRevoke)




	mux.HandleFunc("POST "+BasePath+"/networks/self/received-grants", a.handleReceivedGrantImport)
	mux.HandleFunc("GET "+BasePath+"/networks/self/received-grants", a.handleReceivedGrantList)
	mux.HandleFunc("GET "+BasePath+"/networks/self/received-grants/{grant_id}", a.handleReceivedGrantGet)
	mux.HandleFunc("DELETE "+BasePath+"/networks/self/received-grants/{grant_id}", a.handleReceivedGrantDelete)


	mux.HandleFunc("POST "+BasePath+"/web/handoff", a.handleWebHandoff)
	mux.HandleFunc("GET /web/session", a.handleWebSession)
	mux.HandleFunc("POST /web/logout", a.handleWebLogout)


	if h := a.webUIHandler(mux); h != nil {
		mux.Handle("/", h)
	}

	return a.withMiddleware(mux)
}


func (a *API) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := newRequestID()
		ctx := context.WithValue(r.Context(), ctxRequestID, requestID)




		if p, err := auth.PrincipalFromRequest(r); err == nil {
			ctx = context.WithValue(ctx, ctxPrincipal, p)
		} else if subject, ok := a.principalFromSession(r); ok {


			ctx = context.WithValue(ctx, ctxPrincipal, auth.Principal{
				Role:    console.RoleViewer,
				Subject: subject + " (web session)",
			})
		}

		w.Header().Set("X-Request-Id", requestID)



		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			if p := recover(); p != nil {
				a.Logger.Error("panic serving request",
					"request_id", requestID, "path", r.URL.Path, "panic", p)
				a.writeJSON(w, http.StatusInternalServerError, Error{
					Code:        CodeInternal,
					Message:     "the control plane failed to handle this request",
					RequestID:   requestID,
					Remediation: "Check the control plane logs for request_id " + requestID + ".",
				})
			}


			level := "info"
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				level = "debug"
			}
			attrs := []any{
				"method", r.Method, "path", r.URL.Path, "status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(), "request_id", requestID,
			}
			if level == "debug" {
				a.Logger.Debug("request", attrs...)
			} else {
				a.Logger.Info("request", attrs...)
			}
		}()

		next.ServeHTTP(rec, r.WithContext(ctx))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}



func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

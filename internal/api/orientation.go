package api

import "hjh/internal/contracts"

type OrientationRequest struct {
	SessionID string `json:"sessionId,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Role      string `json:"role,omitempty"`
}

type Orientation = contracts.Orientation

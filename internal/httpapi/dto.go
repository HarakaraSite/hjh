package httpapi

import (
	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/credentials"
	"hjh/internal/extensions"
	"hjh/internal/history"
	"hjh/internal/providers"
)

func sessionDTO(value history.Session) api.Session {
	return api.Session{
		ID: value.ID, WorkspaceRoot: value.WorkspaceRoot, AgentChoice: value.AgentChoice, Agent: value.Agent,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, Title: value.Title, StateRevision: value.StateRevision,
		NextTurn: value.NextTurn, ActiveModel: value.ActiveModel, MessageCount: value.MessageCount,
		ModelChangeCount: value.ModelChangeCount, TurnCount: value.TurnCount, PrivateStateFromTurn: value.PrivateStateFromTurn,
	}
}

func executionDTO(value history.Execution) api.Execution {
	return api.Execution{
		ID: value.ID, TaskID: value.TaskID, Task: value.Task, SessionID: value.SessionID,
		SessionCorrelation: value.SessionCorrelation, ParentExecutionID: value.ParentExecutionID,
		SpawnCallID: value.SpawnCallID, Turn: value.Turn, BaseRevision: value.BaseRevision,
		BaseMessageCount: value.BaseMessageCount, CreatedAt: value.CreatedAt, SettledAt: value.SettledAt,
		AgentName: value.AgentName, Model: value.Model, Build: value.Build, ConfigurationID: value.ConfigurationID,
		MaxSteps: value.MaxSteps, Lifecycle: value.Lifecycle, Outcome: value.Outcome, Adoption: value.Adoption,
		TerminalRecordID: value.TerminalRecordID,
	}
}

func executionsDTO(values []history.Execution) []api.Execution {
	result := make([]api.Execution, len(values))
	for i := range values {
		result[i] = executionDTO(values[i])
	}
	return result
}

func messageDTO(value history.Message) api.Message {
	return api.Message{ExecutionID: value.ExecutionID, Ordinal: value.Ordinal, TurnNumber: value.TurnNumber, JSON: value.JSON}
}

func messagesDTO(values []history.Message) []api.Message {
	result := make([]api.Message, len(values))
	for i := range values {
		result[i] = messageDTO(values[i])
	}
	return result
}

func recordDTO(value history.Record) api.Record {
	relations := make([]api.Relation, len(value.Relations))
	for i, relation := range value.Relations {
		relations[i] = api.Relation{Relation: relation.Relation, TargetRecordID: relation.TargetRecordID,
			Mandatory: relation.Mandatory, Resolved: relation.Resolved}
	}
	return api.Record{ID: value.ID, ExecutionID: value.ExecutionID, Ordinal: value.Ordinal, Kind: value.Kind,
		ObservedAt: value.ObservedAt, Payload: value.Payload, Content: value.Content, Relations: relations}
}

func inspectionDTO(value app.Inspection) api.Inspection {
	children := executionsDTO(value.Children)
	messages := messagesDTO(value.Messages)
	records := make([]api.Record, len(value.Records))
	for i := range value.Records {
		records[i] = recordDTO(value.Records[i])
	}
	return api.Inspection{Execution: executionDTO(value.Execution), Children: children, Messages: messages,
		Records: records, Configuration: value.Configuration, Context: value.Context}
}

func rolesDTO(value extensions.RoleCatalog) api.RoleCatalog {
	roles := make([]api.RoleInfo, len(value.Roles))
	for i, role := range value.Roles {
		roles[i] = api.RoleInfo{Name: role.Name, Description: role.Description, Tools: role.Tools}
	}
	return api.RoleCatalog{DefaultRole: value.DefaultRole, Roles: roles}
}

func providerDTO(value providers.Definition) api.ProviderDefinition {
	models := make([]api.ModelInfo, len(value.Models))
	for i := range value.Models {
		models[i] = modelDTO(value.Models[i])
	}
	return api.ProviderDefinition{ID: value.ID, DisplayName: value.DisplayName, API: value.API, BaseURL: value.BaseURL,
		Path: value.Path, Auth: api.ProviderAuth{Kind: value.Auth.Kind, Profile: value.Auth.Profile},
		DefaultModel: value.DefaultModel, DefaultEffort: value.DefaultEffort, Models: models}
}

func modelDTO(value providers.ModelInfo) api.ModelInfo {
	return api.ModelInfo{ID: value.ID, Name: value.Name}
}

func statusDTO(value credentials.Status) api.Status {
	return api.Status{Connected: value.Connected, Identity: value.Identity, Account: value.Account,
		ExpiresAt: value.ExpiresAt, PlanUsageEnabled: value.PlanUsageEnabled}
}

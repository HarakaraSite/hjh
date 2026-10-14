# Provider and model configuration

Provider routes, credentials references, and model defaults are configured in
the extension `config.json`. The same captured file supplies the role
configuration and the provider registry for a run. Standard routes are ordinary
JSON definitions; Go code does not register them by provider ID.

Responses requests include `reasoning.summary: "auto"`; an explicitly selected
effort is sent alongside it. Public reasoning text and summaries are separate
conversation items. A provider returning no readable reasoning creates neither
item; encrypted replay data remains provider state.
Summaries are requested explicitly, streamed when returned, saved in history,
and shown in the TUI as `thinking summary`. Availability depends on the model's
response, as described in the [OpenAI reasoning guide](https://developers.openai.com/api/docs/guides/reasoning#reasoning-summaries).

Responses function definitions explicitly send `strict: false`, including functions
inside a namespace. This preserves the extension schema's required and optional
arguments instead of allowing upstream strict-mode normalization to require every
field. The parameter schema itself is unchanged. This follows the
[OpenAI function-calling contract](https://developers.openai.com/api/docs/guides/function-calling#strict-mode).
Chat Completions retains its non-strict default.

Tool results match calls by ID and omit the redundant `name` field on every
route: Chat Completions sends `role`, `tool_call_id`, and `content`; Responses
sends `type`, `call_id`, and `output`, retaining a configured namespace.
Tool declarations and calls keep their names, as do saved tool results and
history displays. This follows the
[OpenAI Chat tool-message shape](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)
and [Responses function-result exchange](https://developers.openai.com/api/docs/guides/function-calling).
OpenRouter Chat permits an optional name, but it is unnecessary for matching
results; OpenCode Go's GLM-5.3-Flash route was observed to reject it.

## Provider, API, and authentication profile

These values identify different parts of a connection:

| Value | Meaning |
| --- | --- |
| Provider ID | The key under `providers`. It selects one destination, authentication method, defaults, and model catalog. |
| `api` | The wire protocol implemented by the adapter, currently `responses` or `chat-completions`. It comes from the selected provider definition; model names do not choose it. |
| `auth.profile` | A reference to a credential profile. The resolved selection records this reference as `authProfile`; it contains no credential value. |

OpenAI API-key access and ChatGPT OAuth both use Responses, but have separate
provider IDs and authentication profiles. OpenCode Go Chat Completions and
Responses are also separate routes, with separate model candidates.

The standard routes shipped in `internal/extensions/assets/config.json` are:

| Provider ID | API | Authentication | Default model |
| --- | --- | --- | --- |
| `openrouter-responses` | `responses` | `api-key` | `deepseek/deepseek-v4.1-flash` |
| `opencode-go-chat` | `chat-completions` | `api-key` | `deepseek-v4.1-flash` |
| `opencode-go-responses` | `responses` | `api-key` | `gpt-6-luna` |
| `openai-responses` | `responses` | `api-key` | `gpt-6-luna` |
| `openai-chatgpt` | `responses` | `chatgpt-oauth` | `gpt-6.1-sol` |

The OpenCode Go model candidates follow its [official endpoint and model
list](https://docs.opencode.ai/docs/go/). ChatGPT models are read from the
selected account's [official model catalog](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference).

## JSON shape

`providers` is a map keyed by provider ID. The key is the ID; a definition does
not need an `id` input field. A listed definition includes that ID when returned
by `hjh providers`.

```json
{
  "model": {
    "provider": "openrouter-responses",
    "modelId": "deepseek/deepseek-v4.1-flash",
    "effort": "auto"
  },
  "credentials": {
    "openrouter-api-key": {
      "env": "OPENROUTER_API_KEY",
      "file": "${XDG_CONFIG_HOME}/hjh/credentials/openrouter-api-key"
    }
  },
  "providers": {
    "openrouter-responses": {
      "displayName": "OpenRouter Responses",
      "api": "responses",
      "baseURL": "https://openrouter.ai/api/v1",
      "path": "/responses",
      "auth": {"kind": "api-key", "profile": "openrouter-api-key"},
      "defaultModel": "deepseek/deepseek-v4.1-flash",
      "defaultEffort": "auto",
      "models": [
        {"id": "deepseek/deepseek-v4.1-flash", "name": "DeepSeek V4.1 Flash"}
      ],
      "adapter": {"store": false, "reasoningReplay": true}
    },
    "openai-chatgpt": {
      "displayName": "ChatGPT Responses",
      "api": "responses",
      "baseURL": "https://api.openai.com/v1",
      "path": "/responses",
      "auth": {"kind": "chatgpt-oauth", "profile": "hjh-chatgpt"},
      "defaultModel": "gpt-6.1-sol",
      "defaultEffort": "auto",
      "catalog": {"kind": "openai-account", "path": "/models"},
      "adapter": {"store": false, "namespaceTools": true, "reasoningReplay": true}
    }
  }
}
```

The excerpt can be added to the existing extension config alongside its
`defaultRole`, role instructions, tools, and hooks. A role may add a partial
selection under its existing entry, for example:

```json
{
  "roles": {
    "reviewer": {
      "revision": "1",
      "description": "Reviews the result.",
      "instruction": "Review the requested change.",
      "tools": ["run_js", "read_file"],
      "model": {"provider": "opencode-go-chat"}
    }
  }
}
```

Provider definitions use these fields:

| Field | Use |
| --- | --- |
| `displayName` | Name shown in provider listings. |
| `api` | Adapter protocol: `responses` or `chat-completions`. |
| `baseURL`, `path` | Endpoint base and request path. |
| `auth.kind`, `auth.profile` | Credential method and reference name. |
| `defaultModel`, `defaultEffort` | Values used when selecting this provider without same-provider values to inherit. `auto` leaves effort at the endpoint's default. |
| `models` | Static `{ "id", "name", "efforts": ["low", "high"] }` choices to display. `efforts` is optional and declares only values confirmed for that model. |
| `catalog.kind`, `catalog.path` | Dynamic model catalog source. `openai-account` reads `/models` for the selected ChatGPT account, displaying entries whose `visibility` is `list`. |
| `headers` | Additional non-secret HTTP headers. Authentication headers are supplied by the selected `auth` method. |
| `sessionHeader` | Header name used to send the current Session ID when a route requires it. |
| `adapter` | Confirmed protocol differences: `store`, `namespaceTools`, `reasoningReplay`, and the Chat Completions `reasoningField`. |

`models` are selection suggestions, not an allowlist. An explicit model ID is
resolved and sent even when it is absent from the displayed candidates. For
OpenCode Go, Chat Completions and Responses use their own route and candidate
list; a model's name does not switch the API.

The `adapter` values map to the shared protocol options. `store` is the
Responses request's store value. `namespaceTools` applies the tool namespace
expected by the selected route. `reasoningReplay` permits provider state to be
replayed for the same destination and model, and `reasoningField` names the
provider-specific Chat Completions field read for public thinking and reasoning state. `headers`
and `sessionHeader` are passed to the transport; keep credential values in
credential references rather than these fields.

The standard OpenCode Go Chat definition sets
`"adapter": {"reasoningField": "reasoning_content", "reasoningReplay": true}`.
This reads thinking text for live progress and saved history, and preserves it
in assistant messages sent back to the same destination, account and model.
The field is declared by the official OpenCode model definitions for
[DeepSeek V4.1 Flash](https://github.com/anomalyco/models.dev/blob/dev/providers/opencode-go/models/deepseek-v4.1-flash.toml),
[MiMo V2.6 Pro](https://github.com/anomalyco/models.dev/blob/dev/providers/opencode-go/models/mimo-v2.6-pro.toml), and
[GLM 5.3](https://github.com/anomalyco/models.dev/blob/dev/providers/opencode-go/models/glm-5.3.toml).
Leaving `reasoningField` empty disables Chat Completions thinking extraction,
even if usage reports reasoning tokens. Other provider definitions should use
their documented field; this does not add automatic field detection.
Existing edited config files are not overwritten by default installation.

All standard Responses definitions set `store: false` and `reasoningReplay: true`.
For the same destination, account and model, this reuses the returned output
items, including reasoning state, before the matching tool result in the next
request. Public summary extraction does not depend on `reasoningReplay`.
The current [OpenAI stateless reasoning contract](https://developers.openai.com/api/docs/guides/reasoning)
returns encrypted reasoning state by default with `store: false`; the legacy
`include: ["reasoning.encrypted_content"]` option is not required.

## Credentials

`credentials` maps profile names to references, not secret values:

```json
{
  "credentials": {
    "openai-api-key": {
      "env": "OPENAI_API_KEY",
      "file": "${XDG_CONFIG_HOME}/hjh/credentials/openai-api-key"
    },
    "opencode-go-api-key": {
      "env": "OPENCODE_GO_API_KEY",
      "file": "${XDG_CONFIG_HOME}/hjh/credentials/opencode-go-api-key"
    }
  }
}
```

For `auth.kind: "api-key"`, hjh uses an explicit `--credential-file`, then a
key registered through the TUI, then the non-empty environment variable named
by `env`, then `file`. Registration belongs to the effective config path and
profile, and never changes the external env/file references. Registered values
are saved beneath the Core's credential state root, which can be selected with
`--credential-state-dir` for serve, run, models, and auth.
The file path may start with `~/`. The standard profiles use
`${XDG_CONFIG_HOME}/hjh/credentials/<profile>`; that explicit prefix resolves
through `os.UserConfigDir()`, using `~/.config` on Linux when XDG_CONFIG_HOME
is unset. Other shell expressions are not expanded.
Either reference may be omitted when the other is used.

Use `auth: {"kind":"none"}` for a route that does not require a credential.
For `auth.kind: "chatgpt-oauth"`, `auth.profile` names the hjh-owned ChatGPT
account. Its tokens are managed by hjh rather than placed in this JSON. An
optional `oauthFile` on the matching credential entry selects the record path;
otherwise hjh saves it under `$XDG_CONFIG_HOME/hjh/credentials/chatgpt/`,
or `~/.config/hjh/credentials/chatgpt/` on Linux when XDG_CONFIG_HOME is unset.
Registered API keys use the same credential directory, under
`api-keys/<config-scope>/<profile-digest>.json`. The explicit
`--credential-state-dir PATH` option uses `PATH/credentials/` for both kinds.
Core registry and model favorites continue to use XDG_STATE_HOME.
Issued client/host registration is stored separately from account tokens, so a
failed initial code exchange can begin fresh authorization with the same
issued client. CLI login prompts again on `invalid_grant`; in the TUI, start
`/login PROVIDER_ID` again. Each request resolves the current OAuth token and
refreshes it when needed. Existing saved login is reused; a new browser
authorization is not required for each launch or implementation check.
The noninteractive CLI owns its callback listeners; with I-05's separate TUI,
Core owns authorization attempts and joins their callback/network jobs and accepted
saves during shutdown. A run captures its first key/account binding and shares
it with children using the same owner; later account selection or key registration
applies to newly prepared runs. OAuth refresh continues on the captured account.

## Selection precedence

Model catalog DTOs include optional `efforts` and `effortSource` (`config` or
`catalog`). The TUI's `/effort` opens a picker containing `auto` and the selected
model's declared values. Direct `/effort VALUE` is not accepted, and an existing
unlisted value is not added to the picker. Missing effort information offers
only `auto`. CLI and child model settings keep their existing explicit fields.

Standard OpenCode Go entries declare per-model `efforts`, including empty lists
for models without effort options, following the publisher's
[OpenCode Go model metadata](https://github.com/anomalyco/models.dev/tree/dev/providers/opencode-go/models).
For example, GLM-5.3 and DeepSeek V4.1 Flash declare `low`, `high`, and `max`;
MiMo V2.6 Pro declares no effort values. The Go
[model-list API](https://opencode.ai/zen/go/v1/models) response inspected on
2026-10-13 supplied IDs but no effort metadata, so the standard configuration
supplies the confirmed values for both Chat and Responses routes. Selected
values are sent as Chat Completions `reasoning_effort` or Responses
`reasoning.effort`; `auto` omits the effort field.
OpenRouter's `data[].reasoning.supported_efforts` is also read as catalog effort
choices, preserving the returned order. An omitted field does not declare effort
choices; explicit `null` declares the gateway effort values. For mandatory
reasoning, that gateway list excludes `none`. This follows the
[per-model reasoning options contract](https://openrouter.ai/docs/guides/best-practices/reasoning-tokens#discovering-per-model-reasoning-options).
The confirmed ChatGPT account catalog field `supported_reasoning_levels[].effort`
supplies model-specific choices in server order. The selected account's model
catalog uses the preview's effective `authProfile` and the existing credential
manager, including ordinary token refresh. Opening a picker never begins login.
The [official account catalog contract](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference)
defines `visibility: "list"`, `slug`, `display_name`, and account-scoped access;
the effort field was verified against the live response on 2026-10-11.

The selected provider definition supplies the API. For each selection, an
explicit field wins; when provider changes, omitted authentication profile,
model, and effort come from that destination's defaults. When provider stays
the same, omitted fields inherit from the base selection.

| Run | Base selection | Higher-priority selection |
| --- | --- | --- |
| New Session | Global `model`, then the selected role's `model`, then the workspace's remembered selection | Explicit CLI or TUI selection |
| Resume Session | Saved Session selection | Explicit CLI or TUI selection |
| Child execution | Parent's actual selection | Child role's `model` |

TUI choices through `/provider`, `/model`, and `/effort` are saved immediately
by Core in `<workspace>/.hjh/selection.json`, without requiring a run. The
provider, model, effort, and auth profile reference are remembered together.
They survive TUI/Core restarts and apply only to new Sessions in that workspace;
resuming a Session uses its saved selection. `/new` also remembers the current
Session's next-run selection before switching. Role and account tokens are not
stored in this file. Explicit CLI flags override these defaults for that run
without updating the remembered choice. With no remembered choice, config
defaults apply. Children continue to inherit the actual parent selection.

Changing models through `/model` resets effort to `auto`; confirming the same
model preserves its effort. Core also uses `auto` when a top-level request
explicitly changes model ID without specifying effort. An explicit effort on
the same request takes precedence.

The selection object can contain `provider`, `api`, `authProfile`, `modelId`,
and `effort`. `roles.<name>.model` may contain only the fields it changes. The
resolver derives `api` from the chosen provider definition even when the
selection object contains an API value.

Saved provider selections are not automatically converted to a new provider
ID or API. If the saved provider ID is absent from the current config, select a
provider defined in that config when resuming.

## CLI and TUI

List routes and model choices, then select a provider and model for a run:

```sh
hjh providers
hjh models --provider opencode-go-chat
hjh run --provider opencode-go-chat --model deepseek-v4.1-flash "Continue the work"
hjh run --session SESSION_ID --provider openai-responses --model gpt-6-luna "Continue with OpenAI"
```

For ChatGPT OAuth, start a login in the same CLI process, then list the models
available to that account:

```sh
hjh auth --provider openai-chatgpt login
hjh models --provider openai-chatgpt
hjh run --provider openai-chatgpt --model gpt-6.1-sol "Continue the work"
```

The login command prints the authorization URL and accepts the pasted browser
callback URL, or an empty line after an automatic local callback. In the TUI,
use `/provider`, `/model`, and `/effort` to open pickers. `/model` searches IDs
and display names while keeping the conversation draft separate. Direct
`/provider PROVIDER_ID` and `/model MODEL_ID` remain available.
Use the pickers to browse provider and model candidates. `/login` opens the authentication
profile picker; `/login PROVIDER_ID` opens its API-key input or ChatGPT account screen.
Account Enter selects a connected slot or authorizes an unconnected slot; `a` adds
and `r` reauthorizes. Callback URLs go into a separate masked input field. The old
`/login complete` and `/login callback` commands direct the user to this screen.
The [I-12 contract](i12-authentication-contract.md) defines storage and operation lifetimes.

Use the same `--session` to continue an existing conversation on another
route. Provider changes take effect for that run; the saved conversation
remains available to the selected route.

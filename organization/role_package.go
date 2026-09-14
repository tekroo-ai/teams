package organization

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/tekroo-ai/teams/kernel"
)

type HandlerDisposition string

const (
	HandlerModel         HandlerDisposition = "MODEL_HANDLER"
	HandlerDeterministic HandlerDisposition = "DETERMINISTIC_HANDLER"
	HandlerObserveOnly   HandlerDisposition = "OBSERVE_ONLY"
)

func (disposition HandlerDisposition) Valid() bool {
	return disposition == HandlerModel || disposition == HandlerDeterministic || disposition == HandlerObserveOnly
}

type RoleResource struct {
	Path      string        `json:"path"`
	SHA256    kernel.Digest `json:"sha256"`
	MediaType string        `json:"media_type"`
}

func (resource RoleResource) Valid(expectedMediaType string) bool {
	return validPackagePath(resource.Path) && resource.SHA256.Valid() && resource.MediaType == expectedMediaType
}

type RoleHandlerBinding struct {
	SubscriptionPurpose     string             `json:"subscription_purpose"`
	MessagePurpose          MessagePurpose     `json:"message_purpose"`
	Disposition             HandlerDisposition `json:"disposition"`
	Resource                *RoleResource      `json:"resource,omitempty"`
	InputSchema             *RoleResource      `json:"input_schema,omitempty"`
	ResultSchema            *RoleResource      `json:"result_schema,omitempty"`
	DeterministicOperation  string             `json:"deterministic_operation,omitempty"`
	AllowedResults          []string           `json:"allowed_results,omitempty"`
	AllowedMessageProposals []string           `json:"allowed_message_proposals,omitempty"`
}

func (binding RoleHandlerBinding) Validate() error {
	if binding.SubscriptionPurpose == "" || len(binding.SubscriptionPurpose) > 128 || !binding.MessagePurpose.Valid() || !binding.Disposition.Valid() {
		return ErrInvalidRoleBundle
	}
	if !sortedUniqueNonempty(binding.AllowedResults, 16, 64) {
		return ErrInvalidRoleBundle
	}
	for _, result := range binding.AllowedResults {
		switch result {
		case "blocked", "completed", "failed", "needs_decision":
		default:
			return ErrInvalidRoleBundle
		}
	}
	if len(binding.AllowedMessageProposals) > 64 || !slices.IsSorted(binding.AllowedMessageProposals) {
		return ErrInvalidRoleBundle
	}
	previous := ""
	for _, messageType := range binding.AllowedMessageProposals {
		if !messageTypePattern.MatchString(messageType) || messageType <= previous {
			return ErrInvalidRoleBundle
		}
		previous = messageType
	}
	switch binding.Disposition {
	case HandlerModel:
		if binding.Resource == nil || !binding.Resource.Valid("text/markdown") || binding.InputSchema == nil || !binding.InputSchema.Valid("application/schema+json") || binding.ResultSchema == nil || !binding.ResultSchema.Valid("application/schema+json") || binding.DeterministicOperation != "" {
			return ErrInvalidRoleBundle
		}
	case HandlerDeterministic:
		if binding.Resource != nil || binding.InputSchema == nil || !binding.InputSchema.Valid("application/schema+json") || binding.ResultSchema == nil || !binding.ResultSchema.Valid("application/schema+json") || binding.DeterministicOperation == "" || len(binding.DeterministicOperation) > 256 {
			return ErrInvalidRoleBundle
		}
	case HandlerObserveOnly:
		if binding.Resource != nil || binding.InputSchema != nil || binding.ResultSchema != nil || binding.DeterministicOperation != "" || len(binding.AllowedMessageProposals) != 0 {
			return ErrInvalidRoleBundle
		}
	}
	return nil
}

type LoadedRoleHandler struct {
	Binding      RoleHandlerBinding
	Instructions string
	InputSchema  json.RawMessage
	ResultSchema json.RawMessage
}

type LoadedRolePackage struct {
	Charter  string
	Handlers map[string]LoadedRoleHandler
}

func (rolePackage LoadedRolePackage) Handler(messageType, subscriptionPurpose string) (LoadedRoleHandler, bool) {
	handler, found := rolePackage.Handlers[messageType]
	return handler, found && handler.Binding.SubscriptionPurpose == subscriptionPurpose
}

func LoadRolePackage(bundlePath string, bundle RoleBundle) (LoadedRolePackage, error) {
	if !filepath.IsAbs(bundlePath) || bundle.SchemaVersion != RolePackageSchemaVersion || bundle.Validate() != nil || bundle.Charter == nil {
		return LoadedRolePackage{}, ErrInvalidRoleBundle
	}
	root, err := resolvedPackageRoot(filepath.Dir(bundlePath))
	if err != nil {
		return LoadedRolePackage{}, errors.Join(ErrInvalidRoleBundle, err)
	}
	charter, err := loadRoleResource(root, *bundle.Charter)
	if err != nil || !utf8Text(charter) {
		return LoadedRolePackage{}, errors.Join(ErrInvalidRoleBundle, err)
	}
	loaded := LoadedRolePackage{Charter: string(charter), Handlers: make(map[string]LoadedRoleHandler, len(bundle.HandlerBindings))}
	for messageType, binding := range bundle.HandlerBindings {
		handler := LoadedRoleHandler{Binding: cloneHandlerBinding(binding)}
		if binding.Resource != nil {
			raw, loadErr := loadRoleResource(root, *binding.Resource)
			if loadErr != nil || !utf8Text(raw) {
				return LoadedRolePackage{}, errors.Join(ErrInvalidRoleBundle, loadErr)
			}
			handler.Instructions = string(raw)
		}
		if binding.InputSchema != nil {
			raw, loadErr := loadRoleResource(root, *binding.InputSchema)
			if loadErr != nil || validateRoleSchema(raw) != nil {
				return LoadedRolePackage{}, errors.Join(ErrInvalidRoleBundle, loadErr)
			}
			handler.InputSchema = append(json.RawMessage(nil), raw...)
		}
		if binding.ResultSchema != nil {
			raw, loadErr := loadRoleResource(root, *binding.ResultSchema)
			if loadErr != nil || validateRoleSchema(raw) != nil {
				return LoadedRolePackage{}, errors.Join(ErrInvalidRoleBundle, loadErr)
			}
			handler.ResultSchema = append(json.RawMessage(nil), raw...)
		}
		loaded.Handlers[messageType] = handler
	}
	return loaded, nil
}

func loadRoleResource(root string, resource RoleResource) ([]byte, error) {
	if !validRoleResource(resource) {
		return nil, ErrInvalidRoleBundle
	}
	candidate := filepath.Join(root, resource.Path)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, ErrInvalidRoleBundle
	}
	raw, err := readBounded(resolved, maximumRoleResourceBytes)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	if kernel.Digest(hex.EncodeToString(digest[:])) != resource.SHA256 {
		return nil, ErrInvalidRoleBundle
	}
	return raw, nil
}

func resolvedPackageRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

func validPackagePath(path string) bool {
	return path != "" && len(path) <= 1024 && !filepath.IsAbs(path) && filepath.Clean(path) == path && path != "." && path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator))
}

func utf8Text(raw []byte) bool {
	return utf8.Valid(raw) && strings.TrimSpace(string(raw)) != ""
}

func validRoleResource(resource RoleResource) bool {
	return resource.Valid("text/markdown") || resource.Valid("application/schema+json")
}

type packageSchemaLoader struct{}

func (packageSchemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema resource is not package-bound: %s", url)
}

func validateRoleSchema(raw []byte) error {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if _, object := document.(map[string]any); !object {
		return ErrInvalidRoleBundle
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseLoader(packageSchemaLoader{})
	const schemaURL = "urn:tekroo:role-package-schema"
	if err := compiler.AddResource(schemaURL, document); err != nil {
		return err
	}
	_, err = compiler.Compile(schemaURL)
	return err
}

func cloneRoleResource(resource *RoleResource) *RoleResource {
	if resource == nil {
		return nil
	}
	copy := *resource
	return &copy
}

func cloneHandlerBinding(binding RoleHandlerBinding) RoleHandlerBinding {
	binding.Resource = cloneRoleResource(binding.Resource)
	binding.InputSchema = cloneRoleResource(binding.InputSchema)
	binding.ResultSchema = cloneRoleResource(binding.ResultSchema)
	binding.AllowedResults = slices.Clone(binding.AllowedResults)
	binding.AllowedMessageProposals = slices.Clone(binding.AllowedMessageProposals)
	return binding
}

func cloneHandlerBindings(bindings map[string]RoleHandlerBinding) map[string]RoleHandlerBinding {
	if bindings == nil {
		return nil
	}
	clone := make(map[string]RoleHandlerBinding, len(bindings))
	for messageType, binding := range bindings {
		clone[messageType] = cloneHandlerBinding(binding)
	}
	return clone
}

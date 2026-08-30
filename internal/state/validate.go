package state

import (
	"fmt"
	"strings"

	v "github.com/go-playground/validator/v10"
)

// The declarative structural layer runs after strict YAML decoding and
// canonicalization, before the semantic domain rules. It enforces the
// structural contract of the v1 schema (non-empty fields, bounded lengths,
// enum membership, absolute paths). Semantic rules that need state-wide
// context (uniqueness, cross-references, installed-version membership)
// stay hand-written in ValidateConfig/ValidateSite so error codes stay
// stable and testable.

// configValidation mirrors ConfigSnapshot's structural constraints.
// Kept separate from the YAML document structs so tag churn on the wire
// schema can never silently change validation semantics.
type configValidation struct {
	SchemaVersion int `validate:"required,eq=1"`
	Owner         struct {
		Username string `validate:"required,min=1,max=256"`
		Group    string `validate:"required,min=1,max=256"`
		Home     string `validate:"required,max=4096,is-abs-path"`
	} `validate:"required"`
	Platform struct {
		System string `validate:"required,eq=x86_64-linux"`
	} `validate:"required"`
	Rebuild struct {
		Mode string `validate:"required,oneof=traditional flake"`
	} `validate:"required"`
}

// siteValidation mirrors SiteConfig's structural constraints.
type siteValidation struct {
	SchemaVersion int    `validate:"required,eq=1"`
	ID            string `validate:"required,min=1,max=128"`
	Domain        string `validate:"required,min=1,max=253"`
	ProjectPath   string `validate:"required,min=2,max=4096,is-abs-path"`
	DocumentRoot  string `validate:"required,min=2,max=4096,is-abs-path"`
	PHP           string `validate:"required"`
	Nginx         struct {
		Handler struct {
			Type string `validate:"required,oneof=template custom generic"`
			Name string `validate:"omitempty,min=1,max=64"`
			Path string `validate:"omitempty,min=1,max=4096"`
		} `validate:"required"`
	} `validate:"required"`
}

// stateValidator is the shared instance; tag parsing happens once at init.
var stateValidator = newStructValidator()

func newStructValidator() *v.Validate {
	val := v.New()
	_ = val.RegisterValidation("is-abs-path", func(fl v.FieldLevel) bool {
		return strings.HasPrefix(fl.Field().String(), "/")
	})
	return val
}

// applyStructural runs the declarative layer and converts failures into the
// package's stable StateError codes so callers and tests see one error type.
func applyStructural(kind string, doc any) error {
	if err := stateValidator.Struct(doc); err != nil {
		var field, rule string
		var verrs v.ValidationErrors
		if ok := asValidationErrors(err, &verrs); ok && len(verrs) > 0 {
			fe := verrs[0]
			field = fe.Field()
			rule = fe.Tag()
		}
		msg := "failed structural validation"
		if field != "" {
			msg = fmt.Sprintf("%s: field %q fails %q", msg, field, rule)
		}
		return newStateError("invalid_"+kind, msg, err)
	}
	return nil
}

// asValidationErrors is a small indirection over errors.As for readability.
func asValidationErrors(err error, target *v.ValidationErrors) bool {
	if e, ok := err.(v.ValidationErrors); ok {
		*target = e
		return true
	}
	return false
}

// structuralConfig maps a canonical ConfigSnapshot onto the declarative layer.
func structuralConfig(cfg ConfigSnapshot) configValidation {
	var out configValidation
	out.SchemaVersion = cfg.SchemaVersion
	out.Owner.Username = cfg.Owner.Username
	out.Owner.Group = cfg.Owner.Group
	out.Owner.Home = cfg.Owner.Home
	out.Platform.System = cfg.Platform.System
	out.Rebuild.Mode = cfg.Rebuild.Mode
	return out
}

// structuralSite maps a canonical SiteConfig onto the declarative layer.
func structuralSite(site SiteConfig) siteValidation {
	var out siteValidation
	out.SchemaVersion = site.SchemaVersion
	out.ID = site.ID
	out.Domain = site.Domain
	out.ProjectPath = site.ProjectPath
	out.DocumentRoot = site.DocumentRoot
	out.PHP = site.PHP
	out.Nginx.Handler.Type = site.Nginx.Handler.Type
	out.Nginx.Handler.Name = site.Nginx.Handler.Name
	out.Nginx.Handler.Path = site.Nginx.Handler.Path
	return out
}

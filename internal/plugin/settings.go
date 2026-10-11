package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/collect/pluginkind"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/secret"
	"github.com/NaejEL/opnview/internal/store"
)

// The settings a plugin declares (A6 of the backend-completion cycle).
//
// A SETTING IS STORED NAMESPACED BY ITS PLUGIN, under <plugin>_<key>: a `setting` row, or, for a
// secret, an `encrypted_credential` row sealed with the key file beside the database, exactly as
// the firewall's API secret is. No endpoint ever returns a secret: the listing says only whether
// one is set. A composed key the core reads itself is refused at load (setting_collision), so a
// plugin never shadows a core setting; the built-in maxmind plugin's keys -- maxmind_account_id
// and the maxmind_licence_key credential -- are the keys the settings surface has always written,
// so that page and the first-run wizard keep working unchanged.

// The setting types.
const (
	SettingInteger = "integer"
	SettingNumber  = "number"
	SettingBoolean = "boolean"
	SettingText    = "text"
	SettingEnum    = "enum"
	SettingSecret  = "secret"
)

// SettingDeclaration is one setting a plugin declares.
type SettingDeclaration struct {
	// Key is the setting's own key, a snake_case word; it is stored as <plugin>_<key>.
	Key string `json:"key"`
	// Type is integer, number, boolean, text, enum or secret.
	Type string `json:"type"`
	// Default is the value an unset setting takes. A secret has none.
	Default any `json:"default,omitempty"`
	// Min and Max bound an integer or a number.
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
	// Values is an enum's vocabulary.
	Values []string `json:"values,omitempty"`
	// Label is the catalogue key naming the setting, which the plugin's own locale/ carries.
	Label string `json:"label,omitempty"`
}

// The closed codes a setting write is refused with.
const (
	SettingRefusalUnknownKey   = "unknown_setting"
	SettingRefusalInvalidValue = "invalid_setting_value"
	SettingRefusalOutOfRange   = "setting_out_of_range"
	SettingRefusalNoKeyFile    = "no_key_file"
)

// SettingRefusal is a setting value refused, with its closed code and the key it names.
type SettingRefusal struct {
	Code string `json:"condition"`
	Key  string `json:"key"`
}

// Error implements error.
func (r *SettingRefusal) Error() string {
	return fmt.Sprintf("plugin: setting %s refused (%s)", r.Key, r.Code)
}

// RefusalSettingCollision is a setting whose stored key the core, or another plugin, already uses.
const RefusalSettingCollision = "setting_collision"

// StorageKey is where a plugin's setting is stored.
func StorageKey(plugin, key string) string { return plugin + "_" + key }

// checkSettings validates a manifest's setting declarations.
func checkSettings(manifest Manifest, takenKeys map[string]string) *Refusal {
	seen := map[string]bool{}
	for _, setting := range manifest.Settings {
		if !namePattern.MatchString(setting.Key) {
			return refuse(manifest.Name, RefusalManifestInvalid, "the setting key %q is not a snake_case word", setting.Key)
		}
		if seen[setting.Key] {
			return refuse(manifest.Name, RefusalManifestInvalid, "the setting %q is declared twice", setting.Key)
		}
		seen[setting.Key] = true
		stored := StorageKey(manifest.Name, setting.Key)
		if config.IsCoreKey(stored) {
			return refuse(manifest.Name, RefusalSettingCollision, "the setting %q would be stored as %q, which the "+
				"core reads itself", setting.Key, stored)
		}
		if owner, taken := takenKeys[settingClaim(stored)]; taken {
			return refuse(manifest.Name, RefusalSettingCollision, "the setting %q would be stored as %q, which the "+
				"plugin %s already declares", setting.Key, stored, owner)
		}
		switch setting.Type {
		case SettingInteger, SettingNumber:
			if setting.Min != nil && setting.Max != nil && *setting.Min > *setting.Max {
				return refuse(manifest.Name, RefusalManifestInvalid, "the setting %q has min above max", setting.Key)
			}
		case SettingEnum:
			if len(setting.Values) == 0 {
				return refuse(manifest.Name, RefusalManifestInvalid, "the enum setting %q lists no values", setting.Key)
			}
		case SettingBoolean, SettingText:
		case SettingSecret:
			if setting.Default != nil {
				return refuse(manifest.Name, RefusalManifestInvalid, "the secret %q declares a default; a secret "+
					"has none", setting.Key)
			}
		default:
			return refuse(manifest.Name, RefusalManifestInvalid, "the setting %q has the type %q, not integer, "+
				"number, boolean, text, enum or secret", setting.Key, setting.Type)
		}
		if (setting.Min != nil || setting.Max != nil) && setting.Type != SettingInteger && setting.Type != SettingNumber {
			return refuse(manifest.Name, RefusalManifestInvalid, "the setting %q has bounds and is not a number",
				setting.Key)
		}
		if setting.Default != nil {
			if _, refusal := parseSetting(setting, fmt.Sprint(setting.Default)); refusal != nil {
				return refuse(manifest.Name, RefusalManifestInvalid, "the default of the setting %q is not a value it "+
					"accepts", setting.Key)
			}
		}
	}
	return nil
}

// parseSetting reads a stored or submitted text as a declared setting's value.
func parseSetting(setting SettingDeclaration, text string) (any, *SettingRefusal) {
	refuse := func(code string) (any, *SettingRefusal) { return nil, &SettingRefusal{Code: code, Key: setting.Key} }
	bounded := func(value float64) *SettingRefusal {
		if (setting.Min != nil && value < *setting.Min) || (setting.Max != nil && value > *setting.Max) {
			return &SettingRefusal{Code: SettingRefusalOutOfRange, Key: setting.Key}
		}
		return nil
	}
	switch setting.Type {
	case SettingInteger:
		value, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			return refuse(SettingRefusalInvalidValue)
		}
		if refusal := bounded(float64(value)); refusal != nil {
			return nil, refusal
		}
		return value, nil
	case SettingNumber:
		value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return refuse(SettingRefusalInvalidValue)
		}
		if refusal := bounded(value); refusal != nil {
			return nil, refusal
		}
		return value, nil
	case SettingBoolean:
		switch text {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return refuse(SettingRefusalInvalidValue)
	case SettingEnum:
		for _, allowed := range setting.Values {
			if text == allowed {
				return text, nil
			}
		}
		return refuse(SettingRefusalInvalidValue)
	}
	return text, nil
}

// SettingValue is one setting as the listing reports it: its declaration, and its value, or for a
// secret whether one is set.
type SettingValue struct {
	SettingDeclaration
	Value any  `json:"value"`
	Set   bool `json:"set"`
}

// SettingStore is what reading and writing settings needs of the database.
type SettingStore interface {
	Setting(ctx context.Context, key string) (string, bool, error)
	SetSetting(ctx context.Context, key, value string, now int64) error
	Credential(ctx context.Context, name string) (store.EncryptedCredential, bool, error)
	SetCredential(ctx context.Context, credential store.EncryptedCredential) error
}

// Sealer seals and opens a secret with the key file beside the database.
type Sealer interface {
	Seal(plaintext []byte) (secret.Ciphertext, error)
	Unseal(sealed secret.Ciphertext) ([]byte, error)
}

// SettingValues reads a plugin's settings for the listing: a secret's value is never read, only
// whether it is set.
func (p *Plugin) SettingValues(ctx context.Context, database SettingStore) ([]SettingValue, error) {
	values := make([]SettingValue, 0, len(p.manifest.Settings)+len(p.manifest.Providers))
	for _, setting := range p.operatorSettings() {
		value := SettingValue{SettingDeclaration: setting, Value: setting.Default}
		stored := StorageKey(p.Name, setting.Key)
		if setting.Type == SettingSecret {
			_, present, err := database.Credential(ctx, stored)
			if err != nil {
				return nil, err
			}
			value.Value, value.Set = nil, present
			values = append(values, value)
			continue
		}
		text, present, err := database.Setting(ctx, stored)
		if err != nil {
			return nil, err
		}
		if present {
			if parsed, refusal := parseSetting(setting, text); refusal == nil {
				value.Value, value.Set = parsed, true
			}
		}
		values = append(values, value)
	}
	return values, nil
}

// The states of a secret, as a script reads them beside its value.
const (
	SecretAbsent        = "absent"
	SecretReady         = "ready"
	SecretUndecryptable = "undecryptable"
)

// ReadSettings reads a plugin's settings as its script sees them: every declared key, its stored
// value or its default, and a secret opened -- or null when it is unset or cannot be opened -- with
// each secret's state, so "not entered" and "entered and unreadable" stay two different facts.
func (p *Plugin) ReadSettings(ctx context.Context, database SettingStore, sealer Sealer) (map[string]any,
	map[string]string, error) {
	settings := map[string]any{}
	states := map[string]string{}
	for _, setting := range p.manifest.Settings {
		stored := StorageKey(p.Name, setting.Key)
		if setting.Type == SettingSecret {
			settings[setting.Key], states[setting.Key] = nil, SecretAbsent
			sealed, present, err := database.Credential(ctx, stored)
			if err != nil {
				return nil, nil, err
			}
			if !present {
				continue
			}
			states[setting.Key] = SecretUndecryptable
			if sealer == nil {
				continue
			}
			opened, err := sealer.Unseal(secret.Ciphertext{Algorithm: sealed.Algorithm, KeyID: sealed.KeyID,
				Nonce: sealed.Nonce, Bytes: sealed.Ciphertext})
			if err == nil {
				settings[setting.Key], states[setting.Key] = string(opened), SecretReady
			}
			continue
		}
		settings[setting.Key] = setting.Default
		text, present, err := database.Setting(ctx, stored)
		if err != nil {
			return nil, nil, err
		}
		if present {
			if parsed, refusal := parseSetting(setting, text); refusal == nil {
				settings[setting.Key] = parsed
			}
		}
	}
	return settings, states, nil
}

// ErrNoSuchPlugin is a settings write naming no loaded plugin.
var ErrNoSuchPlugin = errors.New("plugin: no such plugin")

// WriteSettings validates and stores settings submitted for a plugin: every key must be declared
// and every value one the declaration accepts, or nothing is written. A secret is sealed with the
// key file; an empty secret clears nothing and is refused, as the settings surface refuses one.
func (p *Plugin) WriteSettings(ctx context.Context, database SettingStore, sealer Sealer, submitted map[string]any,
	now int64) error {
	declared := map[string]SettingDeclaration{}
	for _, setting := range p.operatorSettings() {
		declared[setting.Key] = setting
	}
	type write struct {
		setting SettingDeclaration
		text    string
	}
	var writes []write
	for _, key := range sortedKeys(submitted) {
		setting, known := declared[key]
		if !known {
			return &SettingRefusal{Code: SettingRefusalUnknownKey, Key: key}
		}
		text, err := settingText(submitted[key])
		if err != nil {
			return &SettingRefusal{Code: SettingRefusalInvalidValue, Key: key}
		}
		if setting.Type == SettingSecret {
			if text == "" {
				return &SettingRefusal{Code: SettingRefusalInvalidValue, Key: key}
			}
			if sealer == nil {
				return &SettingRefusal{Code: SettingRefusalNoKeyFile, Key: key}
			}
		} else if _, refusal := parseSetting(setting, text); refusal != nil {
			return refusal
		}
		writes = append(writes, write{setting: setting, text: text})
	}
	for _, pending := range writes {
		stored := StorageKey(p.Name, pending.setting.Key)
		if pending.setting.Type == SettingSecret {
			sealed, err := sealer.Seal([]byte(pending.text))
			if err != nil {
				return err
			}
			if err := database.SetCredential(ctx, store.EncryptedCredential{Name: stored, Algorithm: sealed.Algorithm,
				KeyID: sealed.KeyID, Nonce: sealed.Nonce, Ciphertext: sealed.Bytes, UpdatedAt: now}); err != nil {
				return err
			}
			continue
		}
		if err := database.SetSetting(ctx, stored, pending.text, now); err != nil {
			return err
		}
	}
	return nil
}

// settingText renders a submitted JSON value as the text a setting row stores.
func settingText(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case bool:
		return strconv.FormatBool(typed), nil
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case json.Number:
		return typed.String(), nil
	}
	return "", errors.New("plugin: a setting value is a string, a number or a boolean")
}

// operatorSettings are what the operator may set on a plugin: the settings its manifest declares,
// and, for each provider stating its own cadence, the override of that cadence -- an integer of
// seconds, never below the provider's lower bound, stored under pluginkind.CadenceSettingKey and
// read by the scheduler before every wait. It carries no label: no catalogue string names it yet,
// and its key, cadence__<kind>__<provider key>, says what it is.
func (p *Plugin) operatorSettings() []SettingDeclaration {
	settings := append([]SettingDeclaration(nil), p.manifest.Settings...)
	for _, declared := range p.manifest.Providers {
		if declared.CadenceSeconds <= 0 {
			continue
		}
		key := declared.Key
		if key == "" {
			key = p.Name
		}
		minimum := float64(max(declared.MinCadenceSeconds, 1))
		settings = append(settings, SettingDeclaration{Key: pluginkind.CadenceSettingName(declared.Kind, key),
			Type: SettingInteger, Default: float64(declared.CadenceSeconds), Min: &minimum})
	}
	return settings
}

// api_microvm_metadata.go — per-VM property bag + UEFI NVRAM editor +
// SSH-key assignments. Source of truth today is the in-memory mocks
// in vm_metadata.go and sshkeys.go ; the day weft-agent grows the
// matching RPCs (SetVMProperty / SetUEFIVar / AssignVMSSHKey, …), the
// handlers below become a thin proxy. The wire stays.

package server

import (
	"context"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// APIVMProperty is the typed wire shape for per-VM annotations.
type APIVMProperty struct {
	Key           string `json:"key" doc:"Operator-defined property key" example:"owner" minLength:"1" maxLength:"128"`
	Value         string `json:"value" doc:"Property value (opaque string ; structured data is the caller's serialisation)" example:"team-alpha"`
	GuestReadable bool   `json:"guest_readable" doc:"When true, the in-guest weft-vm-agent can read this via NATS"`
	UpdatedAt     string `json:"updated_at" doc:"RFC3339, server-stamped" readOnly:"true"`
}

func toAPIVMProperty(p VMProperty) APIVMProperty {
	return APIVMProperty{Key: p.Key, Value: p.Value, GuestReadable: p.GuestReadable, UpdatedAt: p.UpdatedAt}
}

// APIUEFIVar is the typed wire shape for one UEFI NVRAM entry.
type APIUEFIVar struct {
	Namespace  string   `json:"namespace" doc:"EFI vendor GUID ; empty defaults to EFI Global Variable" example:"8be4df61-93ca-11d2-aa0d-00e098032b8c"`
	Name       string   `json:"name" doc:"Variable name" example:"BootOrder" minLength:"1" maxLength:"128"`
	ValueHex   string   `json:"value_hex" doc:"Hex of raw bytes ; empty = empty value (still valid). Whitespace stripped server-side." example:"0000"`
	Attributes []string `json:"attributes" doc:"UEFI attribute flags" example:"[\"NonVolatile\",\"BootServiceAccess\",\"RuntimeAccess\"]"`
	UpdatedAt  string   `json:"updated_at" doc:"RFC3339, server-stamped" readOnly:"true"`
}

func toAPIUEFIVar(v UEFIVar) APIUEFIVar {
	return APIUEFIVar{Namespace: v.Namespace, Name: v.Name, ValueHex: v.ValueHex, Attributes: v.Attributes, UpdatedAt: v.UpdatedAt}
}

// APIVMSSHKey is the typed wire shape for one assigned SSH key. All
// fields except Name + AddedAt are resolved from the catalogue at
// read time ; the SPA shows the comment / fingerprint / type from
// the resolved row.
type APIVMSSHKey struct {
	Name        string `json:"name" doc:"Catalogue entry name"`
	Fingerprint string `json:"fingerprint" doc:"SHA256:<base64-no-pad>, resolved from the catalogue" readOnly:"true"`
	Type        string `json:"type" doc:"Algorithm (ssh-ed25519, ssh-rsa, …)" readOnly:"true"`
	PublicKey   string `json:"public_key" doc:"Full OpenSSH-format line" readOnly:"true"`
	Comment     string `json:"comment" doc:"OpenSSH comment ; falls back to the catalogue description" readOnly:"true"`
	AddedAt     string `json:"added_at" doc:"RFC3339 — when the assignment was made" readOnly:"true"`
}

func toAPIVMSSHKey(k VMSSHKey) APIVMSSHKey {
	return APIVMSSHKey{
		Name: k.Name, Fingerprint: k.Fingerprint, Type: k.Type,
		PublicKey: k.PublicKey, Comment: k.Comment, AddedAt: k.AddedAt,
	}
}

// mountMicroVMMetadataAPI registers the per-VM properties / UEFI /
// SSH-key-assignment endpoints. All operate on the {name} path
// parameter ; the underlying stores are keyed on the VM name.
func mountMicroVMMetadataAPI(api huma.API) {
	mountVMPropertyAPI(api)
	mountUEFIVarAPI(api)
	mountVMSSHKeyAssignAPI(api)
}

// ---- Properties --------------------------------------------------

func mountVMPropertyAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-vm-properties",
		Method:      "GET",
		Path:        "/api/microvms/{name}/properties",
		Summary:     "List per-VM application-level properties",
		Tags:        []string{"microvms", "properties"},
	}, func(_ context.Context, in *vmNameInput) (*listVMPropertiesOutput, error) {
		vmPropsMu.Lock()
		defer vmPropsMu.Unlock()
		props := vmProps[in.Name]
		out := &listVMPropertiesOutput{}
		out.Body = make([]APIVMProperty, 0, len(props))
		for _, p := range props {
			out.Body = append(out.Body, toAPIVMProperty(p))
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-vm-property",
		Method:      "POST",
		Path:        "/api/microvms/{name}/properties",
		Summary:     "Create or replace a per-VM property (upsert)",
		Tags:        []string{"microvms", "properties"},
	}, func(_ context.Context, in *setVMPropertyInput) (*setVMPropertyOutput, error) {
		key := strings.TrimSpace(in.Body.Key)
		if key == "" {
			return nil, huma.Error400BadRequest("key is required")
		}
		entry := VMProperty{
			Key: key, Value: in.Body.Value, GuestReadable: in.Body.GuestReadable,
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		vmPropsMu.Lock()
		defer vmPropsMu.Unlock()
		props := vmProps[in.Name]
		for i, p := range props {
			if p.Key == key {
				props[i] = entry
				vmProps[in.Name] = props
				return &setVMPropertyOutput{Body: toAPIVMProperty(entry)}, nil
			}
		}
		vmProps[in.Name] = append(props, entry)
		return &setVMPropertyOutput{Body: toAPIVMProperty(entry)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-vm-property",
		Method:      "DELETE",
		Path:        "/api/microvms/{name}/properties/{key}",
		Summary:     "Delete a per-VM property",
		Tags:        []string{"microvms", "properties"},
	}, func(_ context.Context, in *deleteVMPropertyInput) (*removedOutput, error) {
		vmPropsMu.Lock()
		defer vmPropsMu.Unlock()
		props := vmProps[in.Name]
		for i, p := range props {
			if p.Key == in.Key {
				vmProps[in.Name] = append(props[:i], props[i+1:]...)
				out := &removedOutput{}
				out.Body.Removed = in.Key
				return out, nil
			}
		}
		return nil, huma.Error404NotFound("no such property: " + in.Key)
	})
}

// ---- UEFI variables ----------------------------------------------

func mountUEFIVarAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-uefi-vars",
		Method:      "GET",
		Path:        "/api/microvms/{name}/uefi-vars",
		Summary:     "List the VM's UEFI NVRAM variables",
		Tags:        []string{"microvms", "uefi"},
	}, func(_ context.Context, in *vmNameInput) (*listUEFIVarsOutput, error) {
		uefiVarsMu.Lock()
		defer uefiVarsMu.Unlock()
		vars := uefiVars[in.Name]
		out := &listUEFIVarsOutput{}
		out.Body = make([]APIUEFIVar, 0, len(vars))
		for _, v := range vars {
			out.Body = append(out.Body, toAPIUEFIVar(v))
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-uefi-var",
		Method:      "POST",
		Path:        "/api/microvms/{name}/uefi-vars",
		Summary:     "Create or replace a UEFI variable (upsert on (namespace, name))",
		Description: "Empty namespace defaults to the EFI Global Variable GUID so the common case (BootOrder, SecureBoot, …) doesn't require typing the GUID. value_hex has whitespace stripped before validation.",
		Tags:        []string{"microvms", "uefi"},
	}, func(_ context.Context, in *setUEFIVarInput) (*setUEFIVarOutput, error) {
		name := strings.TrimSpace(in.Body.Name)
		if name == "" {
			return nil, huma.Error400BadRequest("name is required")
		}
		ns := strings.TrimSpace(in.Body.Namespace)
		if ns == "" {
			ns = efiGlobalNS
		}
		valueHex := strings.ReplaceAll(strings.TrimSpace(in.Body.ValueHex), " ", "")
		if !validHex(valueHex) {
			return nil, huma.Error400BadRequest("value_hex must be a (possibly empty) sequence of hex pairs")
		}
		entry := UEFIVar{
			Namespace: ns, Name: name, ValueHex: valueHex,
			Attributes: append([]string(nil), in.Body.Attributes...),
			UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
		}
		uefiVarsMu.Lock()
		defer uefiVarsMu.Unlock()
		vars := uefiVars[in.Name]
		for i, v := range vars {
			if v.Namespace == entry.Namespace && v.Name == entry.Name {
				vars[i] = entry
				uefiVars[in.Name] = vars
				return &setUEFIVarOutput{Body: toAPIUEFIVar(entry)}, nil
			}
		}
		uefiVars[in.Name] = append(vars, entry)
		return &setUEFIVarOutput{Body: toAPIUEFIVar(entry)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-uefi-var",
		Method:      "DELETE",
		Path:        "/api/microvms/{name}/uefi-vars/{ns}/{varname}",
		Summary:     "Delete a UEFI variable",
		Tags:        []string{"microvms", "uefi"},
	}, func(_ context.Context, in *deleteUEFIVarInput) (*removedOutput, error) {
		uefiVarsMu.Lock()
		defer uefiVarsMu.Unlock()
		vars := uefiVars[in.Name]
		for i, v := range vars {
			if v.Namespace == in.Ns && v.Name == in.Varname {
				uefiVars[in.Name] = append(vars[:i], vars[i+1:]...)
				out := &removedOutput{}
				out.Body.Removed = in.Varname
				return out, nil
			}
		}
		return nil, huma.Error404NotFound("no such variable: " + in.Varname)
	})
}

// ---- VM SSH-key assignments --------------------------------------

func mountVMSSHKeyAssignAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-vm-keys",
		Method:      "GET",
		Path:        "/api/microvms/{name}/keys",
		Summary:     "List the VM's assigned SSH keys (resolved against the catalogue)",
		Tags:        []string{"microvms", "ssh-keys"},
	}, func(_ context.Context, in *vmNameInput) (*listVMSSHKeysOutput, error) {
		vmKeysMu.Lock()
		names := append([]string(nil), vmKeyAssignments[in.Name]...)
		addedMap := map[string]string{}
		for k, v := range vmKeyAddedAt[in.Name] {
			addedMap[k] = v
		}
		vmKeysMu.Unlock()

		out := &listVMSSHKeysOutput{}
		out.Body = make([]APIVMSSHKey, 0, len(names))
		for _, cn := range names {
			k, ok := resolveVMKey(nil, cn, addedMap[cn])
			if !ok {
				continue
			}
			out.Body = append(out.Body, toAPIVMSSHKey(k))
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "add-vm-key",
		Method:      "POST",
		Path:        "/api/microvms/{name}/keys",
		Summary:     "Assign a catalogue SSH key to the VM",
		Description: "Idempotent — re-assigning an existing name is a no-op. The catalogue name must already exist ; unknown names are rejected with a 400.",
		Tags:        []string{"microvms", "ssh-keys"},
	}, func(ctx context.Context, in *addVMKeyInput) (*addVMKeyOutput, error) {
		if in.Body.Name == "" {
			return nil, huma.Error400BadRequest("name is required (the catalogue entry to assign)")
		}
		if _, ok := sshKeysCatalogue.Get(ctx, in.Body.Name); !ok {
			return nil, huma.Error400BadRequest("no such key in catalogue : " + in.Body.Name + " — create it on the SSH Keys page first")
		}

		vmKeysMu.Lock()
		defer vmKeysMu.Unlock()
		for _, existing := range vmKeyAssignments[in.Name] {
			if existing == in.Body.Name {
				addedAt := ""
				if vmKeyAddedAt[in.Name] != nil {
					addedAt = vmKeyAddedAt[in.Name][in.Body.Name]
				}
				k, _ := resolveVMKey(nil, in.Body.Name, addedAt)
				return &addVMKeyOutput{Body: toAPIVMSSHKey(k)}, nil
			}
		}
		vmKeyAssignments[in.Name] = append(vmKeyAssignments[in.Name], in.Body.Name)
		if vmKeyAddedAt[in.Name] == nil {
			vmKeyAddedAt[in.Name] = map[string]string{}
		}
		now := nowRFC3339()
		vmKeyAddedAt[in.Name][in.Body.Name] = now
		k, _ := resolveVMKey(nil, in.Body.Name, now)
		return &addVMKeyOutput{Body: toAPIVMSSHKey(k)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-vm-keys",
		Method:      "PUT",
		Path:        "/api/microvms/{name}/keys",
		Summary:     "Replace the VM's assigned SSH-key set",
		Description: "Replace-set semantics. Unknown names cause the whole call to fail (no partial saves) so the operator never sees a half-applied state.",
		Tags:        []string{"microvms", "ssh-keys"},
	}, func(ctx context.Context, in *setVMKeysInput) (*listVMSSHKeysOutput, error) {
		for _, n := range in.Body.Names {
			if _, ok := sshKeysCatalogue.Get(ctx, n); !ok {
				return nil, huma.Error400BadRequest("no such key in catalogue : " + n)
			}
		}

		vmKeysMu.Lock()
		defer vmKeysMu.Unlock()
		old := map[string]string{}
		if vmKeyAddedAt[in.Name] != nil {
			old = vmKeyAddedAt[in.Name]
		}
		fresh := map[string]string{}
		now := nowRFC3339()
		for _, n := range in.Body.Names {
			if t, ok := old[n]; ok {
				fresh[n] = t
			} else {
				fresh[n] = now
			}
		}
		vmKeyAssignments[in.Name] = append([]string(nil), in.Body.Names...)
		vmKeyAddedAt[in.Name] = fresh

		out := &listVMSSHKeysOutput{}
		out.Body = make([]APIVMSSHKey, 0, len(in.Body.Names))
		for _, n := range in.Body.Names {
			if k, ok := resolveVMKey(nil, n, fresh[n]); ok {
				out.Body = append(out.Body, toAPIVMSSHKey(k))
			}
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "remove-vm-key",
		Method:      "DELETE",
		Path:        "/api/microvms/{name}/keys/{key_name}",
		Summary:     "Remove a key assignment from the VM",
		Tags:        []string{"microvms", "ssh-keys"},
	}, func(_ context.Context, in *removeVMKeyInput) (*removedOutput, error) {
		vmKeysMu.Lock()
		defer vmKeysMu.Unlock()
		assigned := vmKeyAssignments[in.Name]
		for i, n := range assigned {
			if n == in.KeyName {
				vmKeyAssignments[in.Name] = append(assigned[:i], assigned[i+1:]...)
				if vmKeyAddedAt[in.Name] != nil {
					delete(vmKeyAddedAt[in.Name], in.KeyName)
				}
				out := &removedOutput{}
				out.Body.Removed = in.KeyName
				return out, nil
			}
		}
		return nil, huma.Error404NotFound("no such assignment: " + in.KeyName)
	})
}

// ---- shared input / output shapes ---------------------------------

type vmNameInput struct {
	Name string `path:"name" doc:"VM name" example:"web-1" minLength:"1" maxLength:"128"`
}

type listVMPropertiesOutput struct {
	Body []APIVMProperty
}

type setVMPropertyInput struct {
	Name string `path:"name" doc:"VM name" example:"web-1" minLength:"1" maxLength:"128"`
	Body APIVMProperty
}

type setVMPropertyOutput struct {
	Body APIVMProperty
}

type deleteVMPropertyInput struct {
	Name string `path:"name" doc:"VM name" minLength:"1" maxLength:"128"`
	Key  string `path:"key" doc:"Property key" minLength:"1" maxLength:"128"`
}

type listUEFIVarsOutput struct {
	Body []APIUEFIVar
}

type setUEFIVarInput struct {
	Name string `path:"name" doc:"VM name" minLength:"1" maxLength:"128"`
	Body APIUEFIVar
}

type setUEFIVarOutput struct {
	Body APIUEFIVar
}

type deleteUEFIVarInput struct {
	Name    string `path:"name" doc:"VM name" minLength:"1" maxLength:"128"`
	Ns      string `path:"ns" doc:"EFI vendor GUID"`
	Varname string `path:"varname" doc:"UEFI variable name" minLength:"1" maxLength:"128"`
}

type listVMSSHKeysOutput struct {
	Body []APIVMSSHKey
}

type addVMKeyInput struct {
	Name string `path:"name" doc:"VM name" minLength:"1" maxLength:"128"`
	Body struct {
		Name string `json:"name" doc:"Catalogue entry name to assign" example:"alice-laptop" minLength:"1" maxLength:"128"`
	}
}

type addVMKeyOutput struct {
	Body APIVMSSHKey
}

type setVMKeysInput struct {
	Name string `path:"name" doc:"VM name" minLength:"1" maxLength:"128"`
	Body struct {
		Names []string `json:"names" doc:"The exact set of catalogue names to assign (replace-set)"`
	}
}

type removeVMKeyInput struct {
	Name    string `path:"name" doc:"VM name" minLength:"1" maxLength:"128"`
	KeyName string `path:"key_name" doc:"Catalogue entry name" minLength:"1" maxLength:"128"`
}

// removedOutput is the shared 200-OK body for idempotent DELETEs
// (properties / uefi-vars / sshkey-assignments). The wire shape is
// {"removed": "<thing-removed>"} ; clients distinguish the resource
// type from the route they hit.
type removedOutput struct {
	Body struct {
		Removed string `json:"removed"`
	}
}

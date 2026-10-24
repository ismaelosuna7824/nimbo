package access

import (
	_ "embed"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

//go:generate go run ./gen/azure

// azureRolesData is the pinned list of Azure built-in roles (gen/azure):
// every role of the official "Azure built-in roles" page with its GUID,
// description and category; the "Privileged" section is Privileged.
//
//go:embed data/azure_roles.json
var azureRolesData []byte

type azureReference struct {
	Source model.AccessSource `json:"source"`
	Roles  []model.AccessRole `json:"roles"`
	byName map[string]int     // lower-case name -> index
	byID   map[string]int
}

var azureRef = sync.OnceValues(func() (*azureReference, error) {
	var r azureReference
	if err := json.Unmarshal(azureRolesData, &r); err != nil {
		return nil, err
	}
	r.byName = make(map[string]int, len(r.Roles))
	r.byID = make(map[string]int, len(r.Roles))
	for i, role := range r.Roles {
		r.byName[strings.ToLower(role.Name)] = i
		r.byID[strings.ToLower(role.ID)] = i
	}
	return &r, nil
})

func init() { RegisterCatalog("azure", azureCatalog) }

// azureSuggestions are the built-in roles for what an identity does on a
// resource type, least privilege first, per level (Read, Write, Manage).
// The first role of a level is the one a grant writes. Data-plane roles
// come from each service's "Azure RBAC" guidance on Microsoft Learn (Key
// Vault RBAC guide, "Authorize access to blobs using Microsoft Entra ID",
// Service Bus / Event Hubs "Authenticate a managed identity", "Azure
// Container Registry roles and permissions", ...).
var azureSuggestions = map[string][3][]string{
	"azurerm_key_vault": {
		{"Key Vault Secrets User", "Key Vault Certificate User", "Key Vault Crypto User", "Key Vault Reader"},
		{"Key Vault Secrets Officer", "Key Vault Certificates Officer", "Key Vault Crypto Officer"},
		{"Key Vault Administrator"},
	},
	"azurerm_storage_account": {
		{"Storage Blob Data Reader", "Storage Queue Data Reader", "Storage Table Data Reader"},
		{"Storage Blob Data Contributor", "Storage Queue Data Contributor", "Storage Table Data Contributor"},
		{"Storage Blob Data Owner", "Storage Account Contributor"},
	},
	"azurerm_storage_container": {
		{"Storage Blob Data Reader"},
		{"Storage Blob Data Contributor"},
		{"Storage Blob Data Owner"},
	},
	"azurerm_storage_queue": {
		{"Storage Queue Data Reader", "Storage Queue Data Message Processor"},
		{"Storage Queue Data Message Sender", "Storage Queue Data Contributor"},
		{"Storage Queue Data Contributor"},
	},
	"azurerm_servicebus_namespace": {
		{"Azure Service Bus Data Receiver"},
		{"Azure Service Bus Data Sender"},
		{"Azure Service Bus Data Owner"},
	},
	"azurerm_servicebus_queue": {
		{"Azure Service Bus Data Receiver"},
		{"Azure Service Bus Data Sender"},
		{"Azure Service Bus Data Owner"},
	},
	"azurerm_servicebus_topic": {
		{"Azure Service Bus Data Receiver"},
		{"Azure Service Bus Data Sender"},
		{"Azure Service Bus Data Owner"},
	},
	"azurerm_servicebus_subscription": {
		{"Azure Service Bus Data Receiver"},
		{},
		{"Azure Service Bus Data Owner"},
	},
	"azurerm_eventhub_namespace": {
		{"Azure Event Hubs Data Receiver"},
		{"Azure Event Hubs Data Sender"},
		{"Azure Event Hubs Data Owner"},
	},
	"azurerm_eventhub": {
		{"Azure Event Hubs Data Receiver"},
		{"Azure Event Hubs Data Sender"},
		{"Azure Event Hubs Data Owner"},
	},
	"azurerm_container_registry": {
		{"AcrPull", "Container Registry Repository Reader"},
		{"AcrPush", "Container Registry Repository Writer"},
		{"Container Registry Repository Contributor", "AcrDelete", "Container Registry Contributor and Data Access Configuration Administrator"},
	},
	"azurerm_app_configuration": {
		{"App Configuration Data Reader"},
		{"App Configuration Data Owner"},
		{"App Configuration Contributor"},
	},
	"azurerm_cosmosdb_account": {
		{"Cosmos DB Account Reader Role"},
		{"Cosmos DB Operator"},
		{"DocumentDB Account Contributor"},
	},
	"azurerm_mssql_server": {
		{"Reader"},
		{"SQL DB Contributor"},
		{"SQL Server Contributor"},
	},
	"azurerm_mssql_database": {
		{"Reader"},
		{"SQL DB Contributor"},
		{},
	},
	"azurerm_search_service": {
		{"Search Index Data Reader"},
		{"Search Index Data Contributor"},
		{"Search Service Contributor"},
	},
	"azurerm_eventgrid_topic": {
		{},
		{"EventGrid Data Sender"},
		{"EventGrid Contributor"},
	},
	"azurerm_log_analytics_workspace": {
		{"Log Analytics Reader"},
		{"Log Analytics Contributor"},
		{},
	},
	"azurerm_resource_group": {
		{"Reader"},
		{"Contributor"},
		{"Owner"},
	},
	"azurerm_subscription": {
		{"Reader"},
		{"Contributor"},
		{"Owner"},
	},
}

// AzureSuggestedRoles is the role a grant writes per level (Read, Write,
// Manage) for a resource type; "" when the level has none.
func AzureSuggestedRoles(resourceType string) [3]string {
	var out [3]string
	for i, roles := range azureSuggestions[resourceType] {
		if len(roles) > 0 {
			out[i] = roles[0]
		}
	}
	return out
}

// AzureRole looks up a built-in role by name (case-insensitive).
func AzureRole(name string) (model.AccessRole, bool) {
	r, err := azureRef()
	if err != nil {
		return model.AccessRole{}, false
	}
	i, ok := r.byName[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return model.AccessRole{}, false
	}
	return r.Roles[i], true
}

// AzureRoleByID looks up a built-in role by its GUID.
func AzureRoleByID(id string) (model.AccessRole, bool) {
	r, err := azureRef()
	if err != nil {
		return model.AccessRole{}, false
	}
	i, ok := r.byID[strings.ToLower(strings.TrimSpace(id))]
	if !ok {
		return model.AccessRole{}, false
	}
	return r.Roles[i], true
}

// azureCatalog serves the built-in roles: with q.ResourceType the roles
// suggested for it first (Read, Write, Manage order, Suggested set), then
// every other role by name; q.Search filters by name, description,
// category or ID.
func azureCatalog(q CatalogQuery) (*model.AccessCatalog, error) {
	r, err := azureRef()
	if err != nil {
		return nil, err
	}
	out := &model.AccessCatalog{Provider: "azure", Source: r.Source}
	search := strings.ToLower(strings.TrimSpace(q.Search))
	match := func(role model.AccessRole) bool {
		return search == "" || strings.Contains(strings.ToLower(role.Name), search) ||
			strings.Contains(strings.ToLower(role.Description), search) ||
			strings.Contains(strings.ToLower(role.Category), search) || strings.Contains(role.ID, search)
	}
	taken := map[string]bool{}
	for i, roles := range azureSuggestions[q.ResourceType] {
		for _, name := range roles {
			idx, ok := r.byName[strings.ToLower(name)]
			if !ok || taken[r.Roles[idx].ID] || !match(r.Roles[idx]) {
				continue
			}
			role := r.Roles[idx]
			role.Suggested = model.AccessLevels[i]
			taken[role.ID] = true
			out.Roles = append(out.Roles, role)
		}
	}
	rest := make([]model.AccessRole, 0, len(r.Roles))
	for _, role := range r.Roles {
		if !taken[role.ID] && match(role) {
			rest = append(rest, role)
		}
	}
	slices.SortStableFunc(rest, func(a, b model.AccessRole) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	out.Roles = append(out.Roles, rest...)
	return out, nil
}

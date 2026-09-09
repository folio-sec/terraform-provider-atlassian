package policy

import (
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func policyResourceAssociationSchema() schema.NestedAttributeObject {
	meta := map[string]schema.Attribute{}
	for _, name := range []string{
		"scheduled_date",
		"migration_start_date_time",
		"migration_end_data_time",
		"atlassian_account_id",
	} {
		meta[name] = schema.StringAttribute{
			Optional: true,
			MarkdownDescription: "Documented API request field `" + name + "`. " +
				"Nonempty associations are not yet supported.",
		}
	}

	return schema.NestedAttributeObject{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Resource ID. Nonempty associations are not yet supported.",
			},
			"application_status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-reported resource application status.",
			},
			"meta": schema.SingleNestedAttribute{
				Optional: true,
				MarkdownDescription: "Resource request metadata. Uses the published request spelling; " +
					"response metadata is available in the data source.",
				Attributes: meta,
			},
			"links": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Resource request links.",
				Attributes: map[string]schema.Attribute{
					"ticket": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Ticket link.",
					},
				},
			},
		},
	}
}

func policyResourceDataSchema() schema.SingleNestedAttribute {
	association := policyResourceAssociationSchema()
	associationTypes := map[string]attr.Type{}
	for name, field := range association.Attributes {
		associationTypes[name] = field.GetType()
	}

	return schema.SingleNestedAttribute{
		Required:            true,
		MarkdownDescription: "Policy request data.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Policy ID returned by the API.",
			},
			"type": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("policy"),
				MarkdownDescription: "API object type; must be `policy`.",
			},
			"attributes": policyResourceAttributesSchema(association, associationTypes),
		},
	}
}

func policyResourceAttributesSchema(
	association schema.NestedAttributeObject,
	associationTypes map[string]attr.Type,
) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Required:            true,
		MarkdownDescription: "Policy attributes.",
		Attributes: map[string]schema.Attribute{
			"type": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Policy type. Only `data-residency` without associated resources " +
					"is currently supported.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			// Live create and update probes require name/status/rule despite the optional
			// fields in the shared upstream request schema. Empty names are accepted.
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Policy name. May be empty.",
			},
			"status": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Policy status: `enabled` or `disabled`.",
			},
			"rule": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "Membership rule.",
				Attributes: map[string]schema.Attribute{
					"in": schema.SetAttribute{
						Required:    true,
						ElementType: types.StringType,
						MarkdownDescription: "One or more data residency realms. " +
							"Duplicate values and order have no membership significance.",
					},
				},
			},
			"resources": schema.SetNestedAttribute{
				Optional: true,
				Computed: true,
				Default: setdefault.StaticValue(types.SetValueMust(
					types.ObjectType{AttrTypes: associationTypes},
					nil,
				)),
				MarkdownDescription: "Resource associations. Must be empty: association changes and " +
					"migrations are not yet supported.",
				NestedObject: association,
			},
		},
	}
}

func policyResourceDataTypes() map[string]attr.Type {
	return policyResourceDataSchema().GetType().(types.ObjectType).AttrTypes
}

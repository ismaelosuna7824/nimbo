package capacity

import (
	"maps"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Pinned on-demand compute prices (USD per hour, Linux, no discounts) of a
// small set of general-purpose and compute-optimized node shapes per
// cloud, ARM and x86. They are a snapshot: the plan always shows the date
// and regions next to the cost. To refresh, update every price and
// PriceDate together from the sources below.
//
// Sources (official only), checked on PriceDate:
//   - AWS us-east-1: the EC2 on-demand Linux price file behind
//     https://aws.amazon.com/ec2/pricing/on-demand/ (b0.p.awsstatic.com
//     meteredUnitMaps ec2-ondemand-without-sec-sel, US East (N. Virginia)).
//   - Azure eastus: the Azure Retail Prices API
//     (https://prices.azure.com/api/retail/prices, Consumption, Linux).
//   - GCP us-central1:
//     https://cloud.google.com/products/compute/pricing/general-purpose.
//   - OCI (same price in every commercial region; us-ashburn-1 shown): the
//     price list behind https://www.oracle.com/cloud/price-list/ (parts
//     B93297/B93298 Ampere A1 OCPU/memory, B93113/B93114 E4 OCPU/memory),
//     billed per OCPU-hour plus per GB-hour; on A1 one OCPU is one vCPU,
//     on E4 one OCPU is two vCPUs. The A1 monthly free allowance (3,000
//     OCPU-hours, 18,000 GB-hours) is not deducted.

// PriceDate is when the table was checked.
const PriceDate = "2026-10-23"

// HoursPerMonth converts hourly prices to monthly (365 × 24 / 12).
const HoursPerMonth = 730

// Shape is a node size of a cloud.
type Shape struct {
	Cloud  string
	Name   string
	Arch   string // arm64 | x86_64
	VCPU   float64
	MemGiB float64
	Hourly float64 // USD, on-demand
}

// Cloud identifiers (model.CloudEstimate.Cloud), in display order.
var Clouds = []string{"aws", "azure", "gcp", "oci"}

// Regions are the regions the prices are for.
var Regions = map[string]string{
	"aws":   "us-east-1",
	"azure": "eastus",
	"gcp":   "us-central1",
	"oci":   "us-ashburn-1",
}

// OCI flexible shape unit rates (USD per hour).
const (
	ociA1OCPU = 0.01
	ociE4OCPU = 0.025
	ociMemGB  = 0.0015
)

// ociFlex prices a flexible OCI shape configuration.
func ociFlex(name, arch string, ocpus, vcpuPerOCPU, memGB, ocpuRate float64) Shape {
	return Shape{Cloud: "oci", Name: name, Arch: arch, VCPU: ocpus * vcpuPerOCPU, MemGiB: memGB, Hourly: ocpus*ocpuRate + memGB*ociMemGB}
}

// Shapes is the pinned price table.
var Shapes = []Shape{
	// AWS: Graviton3 general purpose (m7g) and compute optimized (c7g),
	// Intel general purpose (m7i).
	{"aws", "m7g.large", "arm64", 2, 8, 0.0816},
	{"aws", "m7g.xlarge", "arm64", 4, 16, 0.1632},
	{"aws", "m7g.2xlarge", "arm64", 8, 32, 0.3264},
	{"aws", "c7g.large", "arm64", 2, 4, 0.0725},
	{"aws", "c7g.xlarge", "arm64", 4, 8, 0.145},
	{"aws", "c7g.2xlarge", "arm64", 8, 16, 0.29},
	{"aws", "m7i.large", "x86_64", 2, 8, 0.1008},
	{"aws", "m7i.xlarge", "x86_64", 4, 16, 0.2016},
	{"aws", "m7i.2xlarge", "x86_64", 8, 32, 0.4032},
	// Azure: Ampere general purpose (Dpsv5), Intel general purpose
	// (Dsv5), compute optimized (Fsv2).
	{"azure", "D2ps_v5", "arm64", 2, 8, 0.077},
	{"azure", "D4ps_v5", "arm64", 4, 16, 0.154},
	{"azure", "D8ps_v5", "arm64", 8, 32, 0.308},
	{"azure", "D2s_v5", "x86_64", 2, 8, 0.096},
	{"azure", "D4s_v5", "x86_64", 4, 16, 0.192},
	{"azure", "D8s_v5", "x86_64", 8, 32, 0.384},
	{"azure", "F2s_v2", "x86_64", 2, 4, 0.0846},
	{"azure", "F4s_v2", "x86_64", 4, 8, 0.169},
	{"azure", "F8s_v2", "x86_64", 8, 16, 0.338},
	// GCP: cost-optimized (e2), balanced (n2), Ampere (t2a).
	{"gcp", "e2-standard-2", "x86_64", 2, 8, 0.06701142},
	{"gcp", "e2-standard-4", "x86_64", 4, 16, 0.13402284},
	{"gcp", "e2-standard-8", "x86_64", 8, 32, 0.26804568},
	{"gcp", "e2-highcpu-4", "x86_64", 4, 4, 0.09894048},
	{"gcp", "n2-standard-2", "x86_64", 2, 8, 0.097118},
	{"gcp", "n2-standard-4", "x86_64", 4, 16, 0.194236},
	{"gcp", "n2-standard-8", "x86_64", 8, 32, 0.388472},
	{"gcp", "t2a-standard-2", "arm64", 2, 8, 0.077},
	{"gcp", "t2a-standard-4", "arm64", 4, 16, 0.154},
	{"gcp", "t2a-standard-8", "arm64", 8, 32, 0.308},
	// OCI: Ampere A1 and AMD E4 flexible shapes at common sizes.
	ociFlex("VM.Standard.A1.Flex 2 OCPU/12 GB", "arm64", 2, 1, 12, ociA1OCPU),
	ociFlex("VM.Standard.A1.Flex 4 OCPU/24 GB", "arm64", 4, 1, 24, ociA1OCPU),
	ociFlex("VM.Standard.A1.Flex 8 OCPU/48 GB", "arm64", 8, 1, 48, ociA1OCPU),
	ociFlex("VM.Standard.E4.Flex 1 OCPU/8 GB", "x86_64", 1, 2, 8, ociE4OCPU),
	ociFlex("VM.Standard.E4.Flex 2 OCPU/16 GB", "x86_64", 2, 2, 16, ociE4OCPU),
	ociFlex("VM.Standard.E4.Flex 4 OCPU/32 GB", "x86_64", 4, 2, 32, ociE4OCPU),
}

// PriceInfo describes the table for a plan.
func PriceInfo() model.PriceTableInfo {
	return model.PriceTableInfo{
		AsOf:    PriceDate,
		Regions: maps.Clone(Regions),
		Note:    "Estimate as of " + PriceDate + ": on-demand Linux compute only (no storage, load balancers, egress, managed control plane or discounts), 730 hours a month.",
		Sources: []string{
			"https://aws.amazon.com/ec2/pricing/on-demand/",
			"https://prices.azure.com/api/retail/prices",
			"https://cloud.google.com/products/compute/pricing/general-purpose",
			"https://www.oracle.com/cloud/price-list/",
		},
	}
}

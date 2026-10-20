# Third-party cloud icons

The SVG files in `assets/` are **not** covered by Nimbo's MIT license. They are
the official architecture-diagram icons of their respective owners, bundled
only so the Architecture view can draw and export architecture diagrams. All
product names, logos and icons are trademarks of their owners; their use does
not imply endorsement. Use them only under the owners' terms below.

Every file was taken unmodified from the official package and only
machine-optimised (SVGO: metadata, comments and editor attributes removed, ids
prefixed per file, numbers rounded to 2 decimals; `viewBox` kept). No icon was
recoloured, cropped, rotated or combined with another icon.

| Prefix | Owner | Source | Version |
|---|---|---|---|
| `aws-*`, `group-aws-*`, `actor-*`, `logo-aws` | Amazon Web Services, Inc. | [AWS Architecture Icons](https://aws.amazon.com/architecture/icons/) — Icon package `Icon-package_07312026` | 2026-07-31 |
| `azure-*`, `logo-azure` | Microsoft Corporation | [Azure architecture icons](https://learn.microsoft.com/azure/architecture/icons/) — `Azure_Public_Service_Icons_V24.zip` | V24 |
| `gcp-*` | Google LLC | [Google Cloud icons](https://cloud.google.com/icons) — `core-products-icons.zip`, `category-icons.zip`, `google-cloud-legacy-icons.zip` | 2025 set + legacy product icons |
| `logo-gcp` | Google LLC | Google Cloud logo served by cloud.google.com (`https://www.gstatic.com/cgc/google-cloud-logo.svg`) | — |
| `oci-*`, `logo-oci` | Oracle and/or its affiliates | [OCI Architecture Diagram Toolkits](https://docs.oracle.com/en-us/iaas/Content/General/Reference/graphicsfordiagrams.htm) — `OCI-Style-Guide-for-Drawio.zip` (OCI Library) | v24.2 toolkit |

The mirrors <https://aws-icons.com/>, <https://az-icons.com/> and
<https://gcpicons.com/> serve the same official sets; the files here come from
the owners' packages directly.

## Usage terms

### Amazon Web Services

> We allow customers and partners to use these toolkits and assets to create
> architecture diagrams. AWS architecture icons are designed to be simple, so
> you can easily use them in diagrams. You can also put icons in materials like
> whitepapers, presentations, data sheets, and posters.
>
> — <https://aws.amazon.com/architecture/icons/>

The `actor-*` files (users, internet, client, server, …) are the generic
resource icons of the same AWS package.

### Microsoft Azure

> Microsoft permits the use of these icons in architectural diagrams, training
> materials, or documentation. You can copy, distribute, and display the icons
> only for the permitted use unless granted explicit permission by Microsoft.
> Microsoft reserves all other rights.
>
> — <https://learn.microsoft.com/azure/architecture/icons/> ("Icon terms")

### Google Cloud

> Welcome to the official library for Google Cloud product icons. Here you can
> find the Google Cloud product icons you need for your diagrams, technical
> documentation, and more.
>
> — <https://cloud.google.com/icons>

Google's brand terms apply to the Google Cloud logo
(<https://about.google/brand-resource-center/>).

### Oracle Cloud Infrastructure

> Use these assets to draw custom architecture diagrams for your OCI
> implementation. Each of these formats contains OCI service icons and
> templates where possible.
>
> — <https://docs.oracle.com/en-us/iaas/Content/General/Reference/graphicsfordiagrams.htm>
> (Copyright © Oracle and/or its affiliates)

Oracle publishes the toolkit as PowerPoint, Visio and draw.io libraries, not as
SVG files. The `oci-*` SVGs are a lossless conversion of the draw.io "OCI
Library" shapes (stencil paths, fills and strokes copied 1:1 into SVG paths);
the text labels of the library shapes are not included. The toolkit has no
Oracle logo shape, so `logo-oci` is the toolkit's "Cloud" glyph.

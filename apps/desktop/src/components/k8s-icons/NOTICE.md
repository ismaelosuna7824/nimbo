# Kubernetes object icons

The SVG files in `assets/` come from the **Kubernetes community icon set**
([kubernetes/community `icons/`](https://github.com/kubernetes/community/tree/master/icons),
commit `f2d487985d7ccd129fc13fdb7be9600091012a99`), the icons the project
publishes to standardize Kubernetes architecture diagrams. The repository is
distributed under the [Apache License 2.0](https://github.com/kubernetes/community/blob/master/LICENSE).
Kubernetes is a registered trademark of The Linux Foundation.

Files used: `svg/resources/unlabeled/<name>.svg` for every resource kind
(`c-role`, `cm`, `crb`, `crd`, `cronjob`, `deploy`, `ds`, `ep`, `group`, `hpa`,
`ing`, `job`, `limits`, `netpol`, `ns`, `pod`, `pv`, `pvc`, `quota`, `rb`,
`role`, `rs`, `sa`, `sc`, `secret`, `sts`, `svc`, `user`, `vol`) and
`svg/infrastructure_components/unlabeled/node.svg`.

Modifications (Apache-2.0 §4b): every file was machine-optimised with SVGO 4.1
(Inkscape/Sodipodi metadata and editor attributes removed, inline `style`
declarations reduced to their fill/stroke presentation attributes, numbers
rounded to 2 decimals, `width`/`height` removed, `viewBox` kept). No glyph was
redrawn or recoloured.

`pdb`, `gateway` and `httproute` have no icon in the set: they are Nimbo badges
made of the set's heptagon background (from `deploy.svg`) with the letters PDB,
GW and RT. The Helm logo used for charts is the Devicon one (see
`../tech-icons/NOTICE.md`).

> Licensed under the Apache License, Version 2.0 (the "License"); you may not
> use these files except in compliance with the License. You may obtain a copy
> of the License at http://www.apache.org/licenses/LICENSE-2.0
>
> Unless required by applicable law or agreed to in writing, software
> distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
> WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
> License for the specific language governing permissions and limitations
> under the License.

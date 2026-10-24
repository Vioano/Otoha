# Source provenance and notices

Otoha incorporates the following owner-requested source ports. Original project
names appear here, in provenance records and in retained legal notices only;
active product names, commands, namespaces and configuration use Otoha.

| Otoha component | Original source | Pinned commit | Upstream license |
| --- | --- | --- | --- |
| Graph | [Vioano/system-atlas](https://github.com/Vioano/system-atlas) | `dae140aeb04d2e5b77e23bb5ff20234f5062fc3d` | [MIT](packages/graph/LICENSE) |
| Native CLI | [Vioano/atlas-cli](https://github.com/Vioano/atlas-cli) | `4c1771d6d9157ceb7afcbf3ca5ad2d24f9320b1b` | [MIT](apps/cli/LICENSE) |
| Collaboration | Owner's local Personal Atlas checkout | `d8e854a0e55137e654073adfdaf7412bef4ef644` | No upstream license file was present; publication was requested by the owner. No blanket relicensing is implied. |
| Mailbox | Local Team Mailbox checkout, originally `NikolaStarx/team-mailbox` | `77581d464fa8d1b0d2182c31bee4fb0a8e03c83a` | [MIT](integrations/mailbox/LICENSE) |

Graph retains the System Atlas, Archify and Cocoon AI copyright notices.
Third-party brand assets retain their own source and rights notes in
[the graph brand catalog](packages/graph/brand-marks/README.md). Unrelated
third-party names are not renamed as Otoha.

The [import manifest](docs/provenance/import-manifest.json) records each selected
source path, commit, original hash, initial transformed hash and final port hash.
The original checkouts, private state, installed services and uncommitted work
are preserved. Production configuration and operational records are excluded.

Preserving names in this file and original legal notices is attribution, not an
active-product alias or a claim that historical upstream validation establishes
Otoha's runtime behavior.

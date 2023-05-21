// Package terradep allows to scan directories containing terraform deployments (main modules) with defined backend.
//
// It detects resource [terraform_remote_state] in other deployments and builds dependency graph. This allows to visualize
// dependencies in your deployments and plan especially when you organize your code in [Terraservices setup]
// and you need orchestrating layer over Terraform.
//
// terradep can represent your dependency graph in two formats:
//   - [Graphviz DOT] - which can be piped to [graph-easy] to generate SVG, PNG or ASCII output
//   - JSON Lines (mostly for debugging)
//
// [terraform_remote_state]: https://developer.hashicorp.com/terraform/language/state/remote
// [Terraservices setup]: https://www.hashicorp.com/resources/evolving-infrastructure-terraform-opencredo
// [Graphviz DOT]: https://graphviz.org/doc/info/lang.html
// [graph-easy]: https://metacpan.org/pod/Graph::Easy
package terradep

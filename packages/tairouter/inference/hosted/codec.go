// SPDX-License-Identifier: Apache-2.0
package hosted

import "github.com/delinoio/oss/packages/tairouter/inference"

// StandardCodec uses the central translator, including opaque reasoning and tool
// metadata. The wrapper never implements a second provider signature codec.
type StandardCodec = inference.HostedCodec

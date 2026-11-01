// SPDX-License-Identifier: Apache-2.0

// Command yuanfang is an AI investigator for production incidents.
package main

import (
	"os"

	"github.com/yinx0004/yuanfang/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}

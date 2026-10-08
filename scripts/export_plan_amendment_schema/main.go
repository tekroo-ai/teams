// Export the runtime's one authoritative schema for contract packaging.
package main

import (
	"os"

	"github.com/tekroo-ai/teams/organization"
)

func main() {
	if _, err := os.Stdout.Write(organization.PlanAmendmentProposalSchema()); err != nil { panic(err) }
}

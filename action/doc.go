// Package action defines the deployment-neutral executable authorization
// contract shared by Domainry hosts and embedded modules.
//
// Definitions contain no handler, principal or persistence implementation.
// Owners register definitions and dynamic permission resolvers during
// assembly, freeze the registry before readiness, and then resolve immutable
// authorization metadata at each executable boundary.
package action

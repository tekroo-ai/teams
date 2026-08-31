package mongo

import (
	"context"
	"errors"

	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var ErrDeploymentIdentityMismatch = errors.New("Teams database deployment identity mismatch")

const deploymentMetadataID = "deployment"

type deploymentIdentity struct {
	Digest kernel.Digest `json:"digest"`
}

func (s *Store) validateDeploymentBeforeInitialization(ctx context.Context, identity kernel.Digest) error {
	var existing metadataDocument
	err := s.db.Collection("metadata").FindOne(ctx, bson.D{{Key: "_id", Value: deploymentMetadataID}}).Decode(&existing)
	if err == nil {
		var bound deploymentIdentity
		if decode(existing.Data, &bound) != nil || bound.Digest != identity {
			return ErrDeploymentIdentityMismatch
		}
		return nil
	}
	if !errors.Is(err, driver.ErrNoDocuments) {
		return err
	}
	clean, err := s.databaseHasNoOrganizationalData(ctx)
	if err != nil {
		return err
	}
	if !clean {
		return ErrDeploymentIdentityMismatch
	}
	return nil
}

// BindDeploymentIdentity makes the no-migration production boundary durable.
// A first binding is accepted only when the database contains no organizational
// data beyond the metadata created by Open. Subsequent starts must present the
// exact same deployment identity.
func (s *Store) BindDeploymentIdentity(ctx context.Context, identity kernel.Digest) error {
	if s == nil || s.db == nil || !identity.Valid() {
		return ErrDeploymentIdentityMismatch
	}
	if err := requireDeadline(ctx); err != nil {
		return err
	}
	var existing metadataDocument
	err := s.db.Collection("metadata").FindOne(ctx, bson.D{{Key: "_id", Value: deploymentMetadataID}}).Decode(&existing)
	if err == nil {
		var bound deploymentIdentity
		if decode(existing.Data, &bound) != nil || bound.Digest != identity {
			return ErrDeploymentIdentityMismatch
		}
		return nil
	}
	if !errors.Is(err, driver.ErrNoDocuments) {
		return err
	}
	clean, err := s.databaseHasNoOrganizationalData(ctx)
	if err != nil {
		return err
	}
	if !clean {
		return ErrDeploymentIdentityMismatch
	}
	data, err := encode(deploymentIdentity{Digest: identity})
	if err != nil {
		return err
	}
	_, err = s.db.Collection("metadata").InsertOne(ctx, metadataDocument{ID: deploymentMetadataID, Data: data})
	if driver.IsDuplicateKeyError(err) {
		err = s.db.Collection("metadata").FindOne(ctx, bson.D{{Key: "_id", Value: deploymentMetadataID}}).Decode(&existing)
		if err != nil {
			return err
		}
		var bound deploymentIdentity
		if decode(existing.Data, &bound) != nil || bound.Digest != identity {
			return ErrDeploymentIdentityMismatch
		}
		return nil
	}
	return err
}

func (s *Store) databaseHasNoOrganizationalData(ctx context.Context) (bool, error) {
	collections, err := s.db.ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return false, err
	}
	for _, name := range collections {
		filter := bson.D{}
		if name == "metadata" {
			filter = bson.D{{Key: "_id", Value: bson.D{{Key: "$nin", Value: bson.A{"kernel", "authorization", "delivery-policy"}}}}}
		}
		count, err := s.db.Collection(name).CountDocuments(ctx, filter, options.Count().SetLimit(1))
		if err != nil {
			return false, err
		}
		if count != 0 {
			return false, nil
		}
	}
	return true, nil
}

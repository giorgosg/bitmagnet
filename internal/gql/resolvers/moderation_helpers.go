package resolvers

import (
	"context"
	"fmt"
	"time"

	"github.com/99designs/gqlgen/graphql"
	gqlauth "github.com/bitmagnet-io/bitmagnet/internal/gql/auth"
	"github.com/bitmagnet-io/bitmagnet/internal/gql/gqlmodel/gen"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
	"github.com/bitmagnet-io/bitmagnet/internal/moderation"
	"github.com/bitmagnet-io/bitmagnet/internal/processor"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
)

func optionalText(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

func moderationRecord(record moderation.Record) gen.ModerationRecord {
	return gen.ModerationRecord{
		Identifier:     record.Identifier.String(),
		Status:         gen.ModerationStatus(record.Status),
		Origin:         string(record.Origin),
		ImportedOrigin: optionalText(string(record.ImportedOrigin)),
		Source:         optionalText(record.Source),
		CreatedAt:      record.CreatedAt,
		UpdatedAt:      record.UpdatedAt,
		Evidence:       record.Evidence,
		Revision:       record.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func moderationPage(limit graphql.Omittable[*int], after graphql.Omittable[*string]) moderation.Page {
	page := moderation.Page{}
	if value, ok := limit.ValueOK(); ok && value != nil {
		page.Limit = *value
	}

	if value, ok := after.ValueOK(); ok && value != nil {
		page.After = *value
	}

	return page
}

func moderationIdentifier(value string) (moderation.Identifier, error) {
	id, err := moderation.ParseIdentifier(moderation.IdentifierFormatV1, value)
	if err != nil {
		return id, moderation.ErrInvalidIdentifier
	}

	return id, nil
}

func moderationActor(ctx context.Context) (string, error) {
	identity, ok := gqlauth.IdentityFromContext(ctx)
	if !ok {
		return "", gqlauth.ErrNotAuthenticated
	}

	self := identity.Self()
	if self.APIKey != nil {
		return fmt.Sprintf("api_key:%d", self.APIKey.ID), nil
	}

	if self.User != nil {
		return fmt.Sprintf("user:%d", self.User.ID), nil
	}

	return "", gqlauth.ErrNotAuthenticated
}

func (r *Resolver) reviewModeration(
	ctx context.Context,
	input gen.ModerationDecisionInput,
	action moderation.ReviewAction,
) (moderation.ReviewResult, error) {
	id, err := moderationIdentifier(input.Identifier)
	if err != nil {
		return moderation.ReviewResult{}, err
	}

	actor, err := moderationActor(ctx)
	if err != nil {
		return moderation.ReviewResult{}, err
	}

	expected, err := time.Parse(time.RFC3339Nano, input.ExpectedRevision)
	if err != nil {
		return moderation.ReviewResult{}, moderation.ErrConflict
	}

	record, err := r.Moderation.Review(
		ctx,
		id,
		action,
		expected,
		actor,
		input.InfoHash.Value(),
		func(hash protocol.ID) (model.QueueJob, error) {
			return processor.NewQueueJob(processor.MessageParams{InfoHashes: []protocol.ID{hash}})
		},
	)

	return record, err
}

func (r *Resolver) changeModeration(
	ctx context.Context,
	input gen.ModerationDecisionInput,
	action moderation.ReviewAction,
) (gen.ModerationRecord, error) {
	result, err := r.reviewModeration(ctx, input, action)
	return moderationRecord(result.Record), err
}

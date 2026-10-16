package enhancement

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExplicitAdmissionSharesCapacityAndPauseWithoutAutomatic(t *testing.T) {
	t.Parallel()
	db, _, hash := applicationFixture(t)
	c := automaticConfig()
	c.Automatic = false
	c.Concurrency = 1
	s := NewScheduler(db.Gorm, c, nil, nil)
	claim, err := s.Request(t.Context(), hash)
	require.NoError(t, err)
	require.NotEmpty(t, claim.Token)
	_, err = s.Request(t.Context(), hash)
	require.ErrorIs(t, err, ErrCapacity)
	require.NoError(t, db.Gorm.Exec("DELETE FROM enhancement_claims").Error)
	require.NoError(
		t,
		db.Gorm.Exec(
			"INSERT INTO key_values(key,value,created_at,updated_at) VALUES "+
				"('enhancement/paused','true',now(),now())",
		).Error,
	)
	_, err = s.Request(t.Context(), hash)
	require.ErrorIs(t, err, ErrPaused)
	require.NoError(t, s.Resume(t.Context()))
	claim, err = s.Request(t.Context(), hash)
	require.NoError(t, err)
	require.NotEmpty(t, claim.Token)
}

func TestExplicitRequestRecoversPaidExpiredOwnerWithoutResettingCaps(t *testing.T) {
	t.Parallel()
	db, _, hash := applicationFixture(t)
	cfg := automaticConfig()
	cfg.Automatic = false
	cfg.Concurrency = 1
	s := NewScheduler(db.Gorm, cfg, nil, nil)
	claim, err := s.Request(t.Context(), hash)
	require.NoError(t, err)
	require.NoError(
		t,
		db.Gorm.Exec(`INSERT INTO classification_evaluations(info_hash,stage,generation,attempt_outcome)
 VALUES (?,'enhancement',2,'running') ON CONFLICT(info_hash,stage)
 DO UPDATE SET generation=2,attempt_outcome='running'`, hash).Error,
	)
	require.NoError(t,
		db.Gorm.Exec(`UPDATE enhancement_claims SET started=true,generation=2,
 input_fingerprint='input',recipe_fingerprint='recipe',expires_at=now()-interval '1 second'
 WHERE token=?`, claim.Token).Error,
	)
	replacement, err := s.Request(t.Context(), hash)
	require.NoError(t, err)
	require.NotEqual(t, claim.Token, replacement.Token)

	var outcome string
	require.NoError(
		t,
		db.Gorm.Raw("SELECT outcome FROM classification_evaluations WHERE info_hash=? AND stage='enhancement'", hash).
			Scan(&outcome).
			Error,
	)
	require.Equal(t, "limited", outcome)
}

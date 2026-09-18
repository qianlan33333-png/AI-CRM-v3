package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
	referralstore "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/store"
)

func TestPostgreSQLReferralAcceptsAndFreezesActivityFacts(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()

	campaign, teamOne, _ := h.createCampaignWithTeams(t, "增长活动一", 101, 201)
	h.joinDirect(t, campaign.ID, teamOne.ID, 101, "join-captain-101")
	inviteA := h.issue(t, campaign.ID, 101, "issue-a-101")

	joinedB := h.joinInvite(t, campaign.ID, 102, inviteA, "join-b-from-a")
	if joinedB.Participation == nil || joinedB.Participation.InviterCustomerID != 101 || joinedB.Participation.TeamID != teamOne.ID {
		t.Fatalf("B participation did not freeze inviter and team: %+v", joinedB.Participation)
	}
	firstRelationship, found, err := h.service.RelationshipAt(context.Background(), 102, h.now)
	if err != nil || !found || firstRelationship.ReferrerCustomerID != 101 || firstRelationship.SourceCampaignID != campaign.ID {
		t.Fatalf("initial relationship=%+v found=%t err=%v", firstRelationship, found, err)
	}

	inviteB := h.issue(t, campaign.ID, 102, "issue-b-102")
	h.joinInvite(t, campaign.ID, 103, inviteB, "join-c-from-b")
	mineA, err := h.service.MyCampaign(context.Background(), referralActor(101, h.clock), campaign.ID)
	if err != nil || mineA.DirectInvitationCount != 1 {
		t.Fatalf("my direct invitation count=%d err=%v", mineA.DirectInvitationCount, err)
	}

	personal, err := h.service.Leaderboard(context.Background(), referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, ViewerCustomerID: 101, Limit: 20})
	if err != nil || len(personal.Items) != 2 || personal.Items[0].CustomerID != 101 || personal.Items[0].Score != 1 || personal.Items[1].CustomerID != 102 || personal.Items[1].Score != 1 {
		t.Fatalf("personal board=%+v err=%v", personal, err)
	}
	for _, period := range []referralport.LeaderboardPeriod{referralport.LeaderboardDay, referralport.LeaderboardWeek} {
		periodBoard, periodErr := h.service.Leaderboard(context.Background(), referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardPersonal, Period: period, Anchor: h.clock, ViewerCustomerID: 101, Limit: 20})
		if periodErr != nil || len(periodBoard.Items) != 2 || periodBoard.Items[0].CustomerID != 101 || periodBoard.Items[1].CustomerID != 102 {
			t.Fatalf("%s board=%+v err=%v", period, periodBoard, periodErr)
		}
	}
	teamBoard, err := h.service.Leaderboard(context.Background(), referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardTeam, Period: referralport.LeaderboardTotal, ViewerTeamID: teamOne.ID, Limit: 20})
	if err != nil || len(teamBoard.Items) != 1 || teamBoard.Items[0].TeamID != teamOne.ID || teamBoard.Items[0].Score != 2 || teamBoard.MyEntry == nil {
		t.Fatalf("team board=%+v err=%v", teamBoard, err)
	}
	inTeam, err := h.service.Leaderboard(context.Background(), referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardInTeam, Period: referralport.LeaderboardTotal, TeamID: teamOne.ID, ViewerCustomerID: 102, Limit: 20})
	if err != nil || len(inTeam.Items) != 2 || inTeam.MyEntry == nil || inTeam.MyEntry.CustomerID != 102 {
		t.Fatalf("in-team board=%+v err=%v", inTeam, err)
	}
	adminReferrals, err := h.admin.ListAdminReferrals(context.Background(), campaign.ID, "", 20)
	if err != nil || len(adminReferrals.Items) != 3 {
		t.Fatalf("admin referrals=%+v err=%v", adminReferrals, err)
	}
	for _, item := range adminReferrals.Items {
		if item.Participation.CustomerID == 101 && (item.Participation.InviterCustomerID != 0 || item.ScoreState != "none") {
			t.Fatalf("direct captain referral exposed a fake inviter or reversed state: %+v", item)
		}
	}

	var creditA int64
	if err = h.pool.QueryRow(context.Background(), `SELECT id FROM referral_score_events WHERE campaign_id=$1 AND inviter_customer_id=101 AND kind='credit'`, campaign.ID).Scan(&creditA); err != nil {
		t.Fatal(err)
	}
	h.clock = h.clock.Add(time.Minute)
	reward, err := h.admin.RecordReward(context.Background(), referralport.RecordRewardCommand{CampaignID: campaign.ID, CustomerID: 101, ScoreEventID: creditA, Period: "total", Reward: "首名礼品", EvidenceReference: "manual:receipt-1", ActorAdminID: 9001, IdempotencyKey: "reward-a-101"})
	if err != nil || reward.ID < 1 {
		t.Fatalf("record reward=%+v err=%v", reward, err)
	}
	if _, err = h.admin.RecordReward(context.Background(), referralport.RecordRewardCommand{CampaignID: campaign.ID, CustomerID: 101, ScoreEventID: creditA, Period: "total", Reward: "首名礼品", EvidenceReference: "manual:receipt-1", ActorAdminID: 9001, IdempotencyKey: "reward-a-101-fresh-key"}); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("fresh-key duplicate reward err=%v", err)
	}

	h.clock = h.clock.Add(time.Minute)
	if err = h.admin.ReverseInvitation(context.Background(), referralport.ReverseInvitationCommand{ParticipationID: joinedB.Participation.ID, Reason: "作弊核实", ActorAdminID: 9001, IdempotencyKey: "reverse-b-102"}); err != nil {
		t.Fatal(err)
	}
	personal, err = h.service.Leaderboard(context.Background(), referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardTotal, ViewerCustomerID: 101, Limit: 20})
	if err != nil || len(personal.Items) != 1 || personal.Items[0].CustomerID != 102 || personal.MyEntry != nil {
		t.Fatalf("reversal did not correct original credit board=%+v err=%v", personal, err)
	}
	dayAfterReversal, err := h.service.Leaderboard(context.Background(), referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardPersonal, Period: referralport.LeaderboardDay, Anchor: h.clock, ViewerCustomerID: 101, Limit: 20})
	if err != nil || len(dayAfterReversal.Items) != 1 || dayAfterReversal.Items[0].CustomerID != 102 {
		t.Fatalf("reversal did not correct original day board=%+v err=%v", dayAfterReversal, err)
	}
	adminView, err := h.admin.ReadAdminCampaign(context.Background(), campaign.ID)
	if err != nil || adminView.InvitationCount != 1 || len(adminView.DailyMetrics) != 1 || adminView.DailyMetrics[0].InviteCount != 1 {
		t.Fatalf("reversal did not correct campaign counts view=%+v err=%v", adminView, err)
	}
	rewards, err := h.admin.ListRewards(context.Background(), campaign.ID, "", 20)
	if err != nil || len(rewards.Items) != 1 || rewards.Items[0].State != referraldomain.RewardNeedsReview {
		t.Fatalf("reversal did not flag recorded reward rewards=%+v err=%v", rewards, err)
	}
	if _, err = h.admin.RecordReward(context.Background(), referralport.RecordRewardCommand{CampaignID: campaign.ID, CustomerID: 101, ScoreEventID: creditA, Period: "total", Reward: "撤销后奖励", EvidenceReference: "manual:receipt-2", ActorAdminID: 9001, IdempotencyKey: "reward-after-reversal"}); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("reversed score event accepted a new reward err=%v", err)
	}

	// A second campaign changes only the current relationship. Campaign one's
	// invitation and score remain immutable even after B accepts the new invite.
	h.clock = h.clock.Add(time.Minute)
	campaignTwo, _, teamTwo := h.createCampaignWithTeams(t, "增长活动二", 301, 201)
	h.joinDirect(t, campaignTwo.ID, teamTwo.ID, 201, "join-captain-201")
	inviteC := h.issue(t, campaignTwo.ID, 201, "issue-c-201")
	h.clock = h.clock.Add(time.Minute)
	h.joinInvite(t, campaignTwo.ID, 102, inviteC, "join-b-from-c")
	current, found, err := h.service.CurrentRelationship(context.Background(), 102)
	if err != nil || !found || current.ReferrerCustomerID != 201 || current.SourceCampaignID != campaignTwo.ID || current.Version != 2 {
		t.Fatalf("current relation was not last accepted fact: %+v found=%t err=%v", current, found, err)
	}
	prior, found, err := h.service.RelationshipAt(context.Background(), 102, h.now.Add(-time.Second))
	if err != nil || !found || prior.ReferrerCustomerID != 101 || prior.SourceCampaignID != campaign.ID {
		t.Fatalf("historical relation changed retroactively: %+v found=%t err=%v", prior, found, err)
	}
	var originalInviter int64
	if err = h.pool.QueryRow(context.Background(), `SELECT inviter_customer_id FROM referral_participations WHERE campaign_id=$1 AND customer_id=102`, campaign.ID).Scan(&originalInviter); err != nil || originalInviter != 101 {
		t.Fatalf("original participation was rewritten inviter=%d err=%v", originalInviter, err)
	}
	if _, err = h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(102, h.clock), CampaignID: campaign.ID, TeamID: teamOne.ID, IdempotencyKey: "join-b-from-a"}); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("same key with a different join payload err=%v", err)
	}
	campaignThree, teamThree, _ := h.createCampaignWithTeams(t, "增长活动三", 401, 402)
	h.joinDirect(t, campaignThree.ID, teamThree.ID, 102, "join-b-without-invite")
	current, found, err = h.service.CurrentRelationship(context.Background(), 102)
	if err != nil || !found || current.ReferrerCustomerID != 201 || current.SourceCampaignID != campaignTwo.ID {
		t.Fatalf("direct participation changed current relation=%+v found=%t err=%v", current, found, err)
	}

	// Once an activity is over, an already-participating member can retry with
	// a fresh idempotency key and receives the immutable original result.
	h.clock = campaign.EndsAt.Add(time.Minute)
	replay, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(102, h.now), CampaignID: campaign.ID, TeamID: teamOne.ID, IdempotencyKey: "replay-after-close-102"})
	if err != nil || replay.Participation == nil || replay.Participation.ID != joinedB.Participation.ID {
		t.Fatalf("post-close repeat did not replay participation=%+v err=%v", replay.Participation, err)
	}

	if err = h.admin.RunCampaignClose(context.Background(), campaign.ID); err != nil {
		t.Fatal(err)
	}
	var state string
	var snapshot map[string]any
	if err = h.pool.QueryRow(context.Background(), `SELECT c.state,s.rankings FROM referral_campaigns c JOIN referral_campaign_snapshots s ON s.campaign_id=c.id WHERE c.id=$1`, campaign.ID).Scan(&state, &snapshot); err != nil || state != string(referraldomain.CampaignEnded) {
		t.Fatalf("close snapshot state=%q snapshot=%v err=%v", state, snapshot, err)
	}
	if _, ok := snapshot["personal"]; !ok {
		t.Fatalf("close snapshot missing ranked personal payload: %v", snapshot)
	}
	if err = h.admin.RunCampaignClose(context.Background(), campaign.ID); err != nil {
		t.Fatalf("replayed close=%v", err)
	}
	var snapshots int
	if err = h.pool.QueryRow(context.Background(), `SELECT count(*) FROM referral_campaign_snapshots WHERE campaign_id=$1`, campaign.ID).Scan(&snapshots); err != nil || snapshots != 1 {
		t.Fatalf("close made duplicate snapshot count=%d err=%v", snapshots, err)
	}
}

func TestPostgreSQLReferralConcurrentAcceptsPreserveOneParticipationAndLastRelation(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()

	campaignOne, teamOne, _ := h.createCampaignWithTeams(t, "并发活动一", 401, 402)
	h.joinDirect(t, campaignOne.ID, teamOne.ID, 401, "join-captain-401")
	inviteOne := h.issue(t, campaignOne.ID, 401, "issue-401")

	// Two accepts for the same activity use different receipt keys. The stable
	// participation lock must still leave one participation and one score.
	commands := []referralport.JoinCampaignCommand{
		{Actor: referralActor(499, h.now), CampaignID: campaignOne.ID, InvitationToken: inviteOne, IdempotencyKey: "concurrent-join-499-a"},
		{Actor: referralActor(499, h.now), CampaignID: campaignOne.ID, InvitationToken: inviteOne, IdempotencyKey: "concurrent-join-499-b"},
	}
	results := runConcurrentJoins(t, h.service, commands)
	if results[0].participationID < 1 || results[0].participationID != results[1].participationID || results[0].err != nil || results[1].err != nil {
		t.Fatalf("same campaign concurrent results=%+v", results)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND customer_id=499`, 1, campaignOne.ID)
	assertCount(t, h.pool, `SELECT count(*) FROM referral_score_events WHERE campaign_id=$1 AND participation_id=$2 AND kind='credit'`, 1, campaignOne.ID, results[0].participationID)

	// Separate campaigns can proceed until they serialize on B's stable current
	// relationship lock. No update is lost: history has two versions and the
	// current row agrees with the final committed history version.
	campaignTwo, teamTwo, _ := h.createCampaignWithTeams(t, "并发活动二", 501, 502)
	h.joinDirect(t, campaignTwo.ID, teamTwo.ID, 501, "join-captain-501")
	inviteTwo := h.issue(t, campaignTwo.ID, 501, "issue-501")
	cross := runConcurrentJoins(t, h.service, []referralport.JoinCampaignCommand{
		{Actor: referralActor(599, h.now), CampaignID: campaignOne.ID, InvitationToken: inviteOne, IdempotencyKey: "cross-join-599-one"},
		{Actor: referralActor(599, h.now), CampaignID: campaignTwo.ID, InvitationToken: inviteTwo, IdempotencyKey: "cross-join-599-two"},
	})
	if cross[0].err != nil || cross[1].err != nil {
		t.Fatalf("cross campaign joins=%+v", cross)
	}
	current, found, err := h.service.CurrentRelationship(context.Background(), 599)
	if err != nil || !found || current.Version != 2 {
		t.Fatalf("concurrent current relation=%+v found=%t err=%v", current, found, err)
	}
	var historyVersion int64
	var historySource int64
	if err = h.pool.QueryRow(context.Background(), `SELECT version,source_campaign_id FROM referral_relationship_history WHERE customer_id=599 ORDER BY version DESC LIMIT 1`).Scan(&historyVersion, &historySource); err != nil || historyVersion != 2 || historySource != current.SourceCampaignID {
		t.Fatalf("history/current mismatch version=%d source=%d current=%+v err=%v", historyVersion, historySource, current, err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_relationship_history WHERE customer_id=599`, 2)
}

func TestPostgreSQLReferralInviterReversalWinsBeforeBlockedNewAcceptance(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	campaign, team, _ := h.createCampaignWithTeams(t, "撤销并发活动", 701, 799)
	h.joinDirect(t, campaign.ID, team.ID, 701, "join-captain-701")
	inviteA := h.issue(t, campaign.ID, 701, "issue-701")
	joinedB := h.joinInvite(t, campaign.ID, 702, inviteA, "join-702-from-701")
	inviteB := h.issue(t, campaign.ID, 702, "issue-702")

	locked := make(chan struct{})
	release := make(chan struct{})
	reversal := make(chan error, 1)
	go func() {
		reversal <- h.uow.Within(context.Background(), func(tx context.Context) error {
			participation, err := h.repository.ReadParticipationByIDWithin(tx, joinedB.Participation.ID, true)
			if err != nil {
				return err
			}
			credit, err := h.repository.ReadCreditScoreEventByParticipationWithin(tx, participation.ID, true)
			if err != nil {
				return err
			}
			close(locked)
			<-release
			if _, err = h.repository.InsertScoreEventWithin(tx, referraldomain.ScoreEvent{CampaignID: campaign.ID, ParticipationID: participation.ID, InviterCustomerID: credit.InviterCustomerID, TeamID: credit.TeamID, Kind: referraldomain.ScoreReverse, Delta: -1, ReversesScoreEventID: credit.ID, Reason: "并发撤销", OccurredAt: h.clock}); err != nil {
				return err
			}
			_, err = h.repository.ReverseParticipationWithin(tx, participation)
			return err
		})
	}()
	select {
	case <-locked:
	case err := <-reversal:
		t.Fatalf("reversal lock failed early: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("reversal did not lock inviter participation")
	}

	accepted := make(chan error, 1)
	go func() {
		_, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(703, h.clock), CampaignID: campaign.ID, InvitationToken: inviteB, IdempotencyKey: "join-703-after-reversal"})
		accepted <- err
	}()
	select {
	case err := <-accepted:
		t.Fatalf("new acceptance bypassed locked inviter participation: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-reversal; err != nil {
		t.Fatal(err)
	}
	if err := <-accepted; !errors.Is(err, referralport.ErrInvitationInvalid) {
		t.Fatalf("reversed inviter accepted a new credit err=%v", err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND customer_id=703`, 0, campaign.ID)
}

func TestPostgreSQLReferralRewardAndReversalSerializeOnTheCredit(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	campaign, team, _ := h.createCampaignWithTeams(t, "奖励竞态活动", 751, 799)
	h.joinDirect(t, campaign.ID, team.ID, 751, "join-captain-751")
	invite := h.issue(t, campaign.ID, 751, "issue-751")
	joined := h.joinInvite(t, campaign.ID, 752, invite, "join-752-from-751")
	var creditID int64
	if err := h.pool.QueryRow(context.Background(), `SELECT id FROM referral_score_events WHERE participation_id=$1 AND kind='credit'`, joined.Participation.ID).Scan(&creditID); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	rewardResult := make(chan error, 1)
	reversalResult := make(chan error, 1)
	go func() {
		<-start
		_, err := h.admin.RecordReward(context.Background(), referralport.RecordRewardCommand{CampaignID: campaign.ID, CustomerID: 751, ScoreEventID: creditID, Period: "total", Reward: "竞态礼品", EvidenceReference: "manual:race", ActorAdminID: 9001, IdempotencyKey: "reward-race-751"})
		rewardResult <- err
	}()
	go func() {
		<-start
		reversalResult <- h.admin.ReverseInvitation(context.Background(), referralport.ReverseInvitationCommand{ParticipationID: joined.Participation.ID, Reason: "竞态撤销", ActorAdminID: 9001, IdempotencyKey: "reverse-race-752"})
	}()
	close(start)
	rewardErr, reversalErr := <-rewardResult, <-reversalResult
	if reversalErr != nil {
		t.Fatalf("reversal err=%v", reversalErr)
	}
	if rewardErr != nil && !errors.Is(rewardErr, referralport.ErrConflict) {
		t.Fatalf("reward err=%v", rewardErr)
	}
	var count int64
	var state string
	err := h.pool.QueryRow(context.Background(), `SELECT count(*),COALESCE(max(state),'') FROM referral_reward_records WHERE score_event_id=$1`, creditID).Scan(&count, &state)
	if err != nil {
		t.Fatal(err)
	}
	if count > 1 || (count == 1 && state != string(referraldomain.RewardNeedsReview)) {
		t.Fatalf("concurrent award escaped reversal review count=%d state=%q rewardErr=%v", count, state, rewardErr)
	}
}

func TestPostgreSQLReferralReversalOnlyReviewsRelatedOrAmbiguousRewards(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	campaign, team, _ := h.createCampaignWithTeams(t, "奖励范围活动", 771, 799)
	h.joinDirect(t, campaign.ID, team.ID, 771, "join-captain-771")
	invite := h.issue(t, campaign.ID, 771, "issue-771-one")
	joinedOne := h.joinInvite(t, campaign.ID, 772, invite, "join-772-from-771")
	invite = h.issue(t, campaign.ID, 771, "issue-771-two")
	joinedTwo := h.joinInvite(t, campaign.ID, 773, invite, "join-773-from-771")
	credits := map[int64]int64{}
	rows, err := h.pool.Query(context.Background(), `SELECT participation_id,id FROM referral_score_events WHERE campaign_id=$1 AND kind='credit'`, campaign.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var participationID, creditID int64
		if err = rows.Scan(&participationID, &creditID); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		credits[participationID] = creditID
	}
	rows.Close()
	if err = rows.Err(); err != nil || credits[joinedOne.Participation.ID] < 1 || credits[joinedTwo.Participation.ID] < 1 {
		t.Fatalf("credits=%v err=%v", credits, err)
	}
	commands := []referralport.RecordRewardCommand{
		{CampaignID: campaign.ID, CustomerID: 771, ScoreEventID: credits[joinedOne.Participation.ID], Period: "total", Reward: "关联一", EvidenceReference: "manual:one", ActorAdminID: 9001, IdempotencyKey: "scope-linked-one"},
		{CampaignID: campaign.ID, CustomerID: 771, ScoreEventID: credits[joinedTwo.Participation.ID], Period: "total", Reward: "关联二", EvidenceReference: "manual:two", ActorAdminID: 9001, IdempotencyKey: "scope-linked-two"},
		{CampaignID: campaign.ID, CustomerID: 771, Period: "total", Reward: "总榜奖励", EvidenceReference: "manual:total", ActorAdminID: 9001, IdempotencyKey: "scope-total"},
		{CampaignID: campaign.ID, CustomerID: 771, Period: "day:2026-09-18", Reward: "当日奖励", EvidenceReference: "manual:day", ActorAdminID: 9001, IdempotencyKey: "scope-day"},
		{CampaignID: campaign.ID, CustomerID: 771, Period: "day:2026-09-17", Reward: "其他日奖励", EvidenceReference: "manual:other-day", ActorAdminID: 9001, IdempotencyKey: "scope-other-day"},
		{CampaignID: campaign.ID, CustomerID: 771, Period: "运营自定义", Reward: "待核查奖励", EvidenceReference: "manual:unknown", ActorAdminID: 9001, IdempotencyKey: "scope-unknown"},
	}
	for _, command := range commands {
		if _, err = h.admin.RecordReward(context.Background(), command); err != nil {
			t.Fatalf("record reward %q: %v", command.Reward, err)
		}
	}
	if err = h.admin.ReverseInvitation(context.Background(), referralport.ReverseInvitationCommand{ParticipationID: joinedOne.Participation.ID, Reason: "关联一撤销", ActorAdminID: 9001, IdempotencyKey: "scope-reverse-one"}); err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	rows, err = h.pool.Query(context.Background(), `SELECT reward,state FROM referral_reward_records WHERE campaign_id=$1`, campaign.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var reward, state string
		if err = rows.Scan(&reward, &state); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		states[reward] = state
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, reward := range []string{"关联一", "总榜奖励", "当日奖励", "待核查奖励"} {
		if states[reward] != string(referraldomain.RewardNeedsReview) {
			t.Fatalf("reward %q state=%q; expected review states=%v", reward, states[reward], states)
		}
	}
	for _, reward := range []string{"关联二", "其他日奖励"} {
		if states[reward] != string(referraldomain.RewardRecorded) {
			t.Fatalf("unrelated reward %q state=%q states=%v", reward, states[reward], states)
		}
	}
	var reason string
	if err = h.pool.QueryRow(context.Background(), `SELECT reason FROM referral_reward_reviews rr JOIN referral_reward_records r ON r.id=rr.reward_id WHERE r.reward='待核查奖励'`).Scan(&reason); err != nil || len(reason) < len("unscoped_reward_requires_review:") || reason[:len("unscoped_reward_requires_review:")] != "unscoped_reward_requires_review:" {
		t.Fatalf("unscoped review reason=%q err=%v", reason, err)
	}
}

func TestPostgreSQLReferralCaptainMustJoinOwnTeamAndIsUniquePerCampaign(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	campaign, captainTeam, otherTeam := h.createCampaignWithTeams(t, "队长归队活动", 761, 762)
	if _, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(761, h.clock), CampaignID: campaign.ID, TeamID: otherTeam.ID, IdempotencyKey: "captain-761-wrong-team"}); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("captain joined another team err=%v", err)
	}
	if _, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(761, h.clock), CampaignID: campaign.ID, TeamID: captainTeam.ID, IdempotencyKey: "captain-761-own-team"}); err != nil {
		t.Fatalf("captain could not join own team err=%v", err)
	}
	draft, err := h.admin.CreateCampaign(context.Background(), referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "队长唯一草稿", StartsAt: h.clock.Add(time.Hour), EndsAt: h.clock.Add(48 * time.Hour), IdempotencyKey: "create-captain-unique-draft"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: draft.ID, CaptainCustomerID: 761, Name: "唯一队长队", IdempotencyKey: "create-captain-unique-team"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: draft.ID, CaptainCustomerID: 761, Name: "重复队长", IdempotencyKey: "duplicate-captain-761"}); !errors.Is(err, referralport.ErrConflict) {
		t.Fatalf("one captain created multiple teams err=%v", err)
	}
}

func TestPostgreSQLReferralRejectsUnsafeInvitationsAndRollsBackPlatformFacts(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	campaign, team, _ := h.createCampaignWithTeams(t, "拒绝活动", 801, 802)
	h.joinDirect(t, campaign.ID, team.ID, 801, "join-captain-801")
	invite := h.issue(t, campaign.ID, 801, "issue-801")

	if _, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(801, h.clock), CampaignID: campaign.ID, InvitationToken: invite, IdempotencyKey: "self-invite-801"}); !errors.Is(err, referralport.ErrInvitationInvalid) {
		t.Fatalf("self invitation err=%v", err)
	}
	if _, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(803, h.clock), CampaignID: campaign.ID, InvitationToken: "rfi_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", IdempotencyKey: "forged-invite-803"}); !errors.Is(err, referralport.ErrInvitationInvalid) {
		t.Fatalf("forged invitation err=%v", err)
	}
	h.clock = h.clock.Add(30 * time.Minute)
	if _, err := h.pool.Exec(context.Background(), `UPDATE referral_invitations SET expires_at=$2 WHERE campaign_id=$1`, campaign.ID, h.clock.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(804, h.clock), CampaignID: campaign.ID, InvitationToken: invite, IdempotencyKey: "expired-invite-804"}); !errors.Is(err, referralport.ErrInvitationInvalid) {
		t.Fatalf("expired invitation err=%v", err)
	}

	if _, err := h.admin.SetCampaignState(context.Background(), referralport.SetCampaignStateCommand{ActorAdminID: 9001, CampaignID: campaign.ID, ExpectedVersion: campaign.Version, Target: referraldomain.CampaignDisabled, IdempotencyKey: "disable-campaign-801"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(805, h.clock), CampaignID: campaign.ID, TeamID: team.ID, IdempotencyKey: "disabled-direct-805"}); !errors.Is(err, referralport.ErrCampaignUnavailable) {
		t.Fatalf("disabled campaign join err=%v", err)
	}

	// A platform outbox failure happens after Referral's participation, receipt,
	// and audit writes. The common UoW must roll all of them back together.
	rollbackCampaign, rollbackTeam, _ := h.createCampaignWithTeams(t, "回滚活动", 901, 902)
	var auditBefore, outboxBefore int64
	if err := h.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE action='referral.participation.joined'`).Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	if err := h.pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE event_type='referral.participation.joined'`).Scan(&outboxBefore); err != nil {
		t.Fatal(err)
	}
	h.service.outbox = failingReferralOutbox{}
	if _, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(903, h.clock), CampaignID: rollbackCampaign.ID, TeamID: rollbackTeam.ID, IdempotencyKey: "outbox-failure-903"}); !errors.Is(err, errReferralOutbox) {
		t.Fatalf("outbox failure join err=%v", err)
	}
	assertCount(t, h.pool, `SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND customer_id=903`, 0, rollbackCampaign.ID)
	assertCount(t, h.pool, `SELECT count(*) FROM referral_operation_receipts WHERE operation='join' AND actor_scope='customer:903'`, 0)
	assertCount(t, h.pool, `SELECT count(*) FROM audit_events WHERE action='referral.participation.joined'`, auditBefore)
	assertCount(t, h.pool, `SELECT count(*) FROM outbox_events WHERE event_type='referral.participation.joined'`, outboxBefore)
}

func TestPostgreSQLReferralRanksAcrossAsiaShanghaiDayAndWeekBoundaries(t *testing.T) {
	h := newReferralPostgreSQLHarness(t)
	defer h.cleanup()
	h.clock = time.Date(2026, 9, 20, 15, 50, 0, 0, time.UTC) // Sunday 23:50 in Shanghai.
	campaign, team, _ := h.createCampaignWithTeams(t, "跨日周活动", 1001, 1999)
	h.joinDirect(t, campaign.ID, team.ID, 1001, "join-captain-1001")
	inviteA := h.issue(t, campaign.ID, 1001, "issue-1001")
	sunday := h.clock
	h.joinInvite(t, campaign.ID, 1002, inviteA, "join-1002-sunday")
	inviteB := h.issue(t, campaign.ID, 1002, "issue-1002")
	h.clock = time.Date(2026, 9, 20, 16, 30, 0, 0, time.UTC) // Monday 00:30 in Shanghai.
	monday := h.clock
	h.joinInvite(t, campaign.ID, 1003, inviteB, "join-1003-monday")

	for _, scenario := range []struct {
		period referralport.LeaderboardPeriod
		anchor time.Time
		winner int64
	}{
		{referralport.LeaderboardDay, sunday, 1001},
		{referralport.LeaderboardDay, monday, 1002},
		{referralport.LeaderboardWeek, sunday, 1001},
		{referralport.LeaderboardWeek, monday, 1002},
	} {
		board, err := h.service.Leaderboard(context.Background(), referralport.LeaderboardQuery{CampaignID: campaign.ID, Kind: referralport.LeaderboardPersonal, Period: scenario.period, Anchor: scenario.anchor, Limit: 20})
		if err != nil || len(board.Items) != 1 || board.Items[0].CustomerID != scenario.winner || board.Items[0].Score != 1 {
			t.Fatalf("period=%s anchor=%s board=%+v err=%v", scenario.period, scenario.anchor, board, err)
		}
	}
}

type referralPostgreSQLHarness struct {
	pool       *pgxpool.Pool
	cleanup    func()
	uow        platformport.UnitOfWork
	repository *referralstore.Repository
	service    *Service
	admin      *AdminService
	now        time.Time
	clock      time.Time
}

func newReferralPostgreSQLHarness(t *testing.T) *referralPostgreSQLHarness {
	t.Helper()
	pool, cleanup := referralPostgreSQLPool(t)
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	t.Cleanup(wrapped.Close)
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	repository, err := referralstore.NewPostgreSQL(pool, uow)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	closeJobs := &referralCloseEnqueuer{}
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	service, err := NewService(uow, repository, "https://referral.test", key, closeJobs, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	admin, err := NewAdminService(uow, repository, referralCustomerVerifier{}, closeJobs, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	clock := time.Date(2026, 9, 18, 1, 30, 0, 0, time.UTC)
	service.now = func() time.Time { return clock }
	admin.now = func() time.Time { return clock }
	h := &referralPostgreSQLHarness{pool: pool, cleanup: cleanup, uow: uow, repository: repository, service: service, admin: admin, clock: clock}
	// Capture current clock through a deliberately shared closure so each test
	// can advance the server time without allowing any browser-supplied time.
	service.now = func() time.Time { return h.clock }
	admin.now = func() time.Time { return h.clock }
	h.now = h.clock
	return h
}

func (h *referralPostgreSQLHarness) createCampaignWithTeams(t *testing.T, name string, captainOne, captainTwo int64) (referraldomain.Campaign, referraldomain.Team, referraldomain.Team) {
	t.Helper()
	starts := h.clock.Add(-time.Hour)
	ends := h.clock.Add(2 * time.Hour)
	campaign, err := h.admin.CreateCampaign(context.Background(), referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: name, StartsAt: starts, EndsAt: ends, IdempotencyKey: "create-" + name})
	if err != nil {
		t.Fatalf("create campaign %q: %v", name, err)
	}
	teamOne, err := h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: campaign.ID, CaptainCustomerID: captainOne, Name: name + "甲队", IdempotencyKey: "team-one-" + name})
	if err != nil {
		t.Fatalf("create first team: %v", err)
	}
	teamTwo, err := h.admin.CreateTeam(context.Background(), referralport.CreateTeamCommand{ActorAdminID: 9001, CampaignID: campaign.ID, CaptainCustomerID: captainTwo, Name: name + "乙队", IdempotencyKey: "team-two-" + name})
	if err != nil {
		t.Fatalf("create second team: %v", err)
	}
	campaign, err = h.admin.SetCampaignState(context.Background(), referralport.SetCampaignStateCommand{ActorAdminID: 9001, CampaignID: campaign.ID, ExpectedVersion: campaign.Version, Target: referraldomain.CampaignActive, IdempotencyKey: "activate-" + name})
	if err != nil {
		t.Fatalf("activate campaign: %v", err)
	}
	return campaign, teamOne, teamTwo
}

func (h *referralPostgreSQLHarness) joinDirect(t *testing.T, campaignID, teamID, customerID int64, key string) {
	t.Helper()
	if _, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(customerID, h.clock), CampaignID: campaignID, TeamID: teamID, IdempotencyKey: key}); err != nil {
		t.Fatalf("direct join customer=%d: %v", customerID, err)
	}
	h.clock = h.clock.Add(time.Minute)
}

func (h *referralPostgreSQLHarness) issue(t *testing.T, campaignID, customerID int64, key string) string {
	t.Helper()
	link, err := h.service.IssueInvitation(context.Background(), referralport.IssueInvitationCommand{Actor: referralActor(customerID, h.clock), CampaignID: campaignID, IdempotencyKey: key})
	if err != nil {
		t.Fatalf("issue invitation customer=%d: %v", customerID, err)
	}
	h.clock = h.clock.Add(time.Minute)
	return link.URL[len("https://referral.test/referral/invite/"):]
}

func (h *referralPostgreSQLHarness) joinInvite(t *testing.T, campaignID, customerID int64, token, key string) referralport.MyCampaign {
	t.Helper()
	joined, err := h.service.JoinCampaign(context.Background(), referralport.JoinCampaignCommand{Actor: referralActor(customerID, h.clock), CampaignID: campaignID, InvitationToken: token, IdempotencyKey: key})
	if err != nil {
		t.Fatalf("invite join customer=%d: %v", customerID, err)
	}
	h.now = h.clock
	h.clock = h.clock.Add(time.Minute)
	return joined
}

type referralCloseEnqueuer struct{}

func (*referralCloseEnqueuer) EnqueueCampaignCloseWithin(context.Context, int64, time.Time) error {
	return nil
}

type referralCustomerVerifier struct{}

func (referralCustomerVerifier) VerifyCanonicalCustomer(context.Context, int64) (bool, error) {
	return true, nil
}

func referralActor(customerID int64, at time.Time) distributionport.TrustedSessionActor {
	return distributionport.TrustedSessionActor{CustomerID: customerID, IdentityID: customerID + 10_000, AppID: "wx-referral-test", AppScope: "wechat-app:wx-referral-test", Channel: "h5_official_account", OccurredAt: at}
}

var errReferralOutbox = errors.New("referral test outbox failure")

type failingReferralOutbox struct{}

func (failingReferralOutbox) Append(context.Context, platformoutbox.Event) (platformoutbox.Event, error) {
	return platformoutbox.Event{}, errReferralOutbox
}

type concurrentJoinResult struct {
	participationID int64
	err             error
}

func runConcurrentJoins(t *testing.T, service *Service, commands []referralport.JoinCampaignCommand) []concurrentJoinResult {
	t.Helper()
	start := make(chan struct{})
	results := make([]concurrentJoinResult, len(commands))
	var wait sync.WaitGroup
	for index := range commands {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			value, err := service.JoinCampaign(context.Background(), commands[index])
			if value.Participation != nil {
				results[index].participationID = value.Participation.ID
			}
			results[index].err = err
		}(index)
	}
	close(start)
	wait.Wait()
	return results
}

func assertCount(t *testing.T, pool *pgxpool.Pool, query string, expected int64, args ...any) {
	t.Helper()
	var actual int64
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&actual); err != nil || actual != expected {
		t.Fatalf("count expected=%d actual=%d err=%v query=%s", expected, actual, err, query)
	}
}

func referralPostgreSQLPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping Referral PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	var randomBytes [8]byte
	if _, err = rand.Read(randomBytes[:]); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_referral_" + hex.EncodeToString(randomBytes[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config := adminConfig.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		pool.Close()
		admin.Close()
		t.Fatal("locate referral migrations")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	for _, name := range []string{"0001_platform.sql", "0183_referral_core.sql"} {
		body, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			pool.Close()
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(body)); execErr != nil {
			pool.Close()
			admin.Close()
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	if _, err = pool.Exec(ctx, `CREATE TABLE outbox_events (
        id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
        aggregate_type TEXT NOT NULL,
        aggregate_id TEXT NOT NULL,
        event_type TEXT NOT NULL,
        event_version SMALLINT NOT NULL,
        idempotency_key TEXT NOT NULL UNIQUE,
        payload_json JSONB NOT NULL,
        occurred_at TIMESTAMPTZ NOT NULL,
        processed_at TIMESTAMPTZ NULL
    )`); err != nil {
		pool.Close()
		admin.Close()
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}

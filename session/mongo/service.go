package mongo

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/UnderTreeTech/waterdrop/pkg/database/mongo"
	"github.com/UnderTreeTech/waterdrop/pkg/log"
	"github.com/UnderTreeTech/waterdrop/pkg/utils/xtime"
	"github.com/google/uuid"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

var (
	errAppNameRequired = fmt.Errorf("app_name is required")
)

const appNameAgentSep = "::"

// mongoSessionService implements session.Service using Mongo as the backend.
type mongoSessionService struct {
	db *mongo.DB
	// NumRecentEvents returns at most NumRecentEvents most recent events.
	// Optional: if zero, the filter is not applied.
	NumRecentEvents int
	// After returns events with timestamp >= the given time.
	// Optional: if zero, the filter is not applied.
	After int64
}

// appIdentity represents parsed app metadata from app_name.
type appIdentity struct {
	appName   string
	agentName string
	agentID   string
}

// NewMongoSessionService creates a new Mongo-backed session service.
func NewMongoSessionService(db *mongo.DB, numRecentEvents int, after int64) session.Service {
	return &mongoSessionService{
		db:              db,
		NumRecentEvents: numRecentEvents,
		After:           after,
	}
}

// Create implements session.Service.Create
func (m *mongoSessionService) Create(ctx context.Context, req *session.CreateRequest) (*session.CreateResponse, error) {
	ident, err := parseAppIdentity(req.AppName)
	if err != nil {
		return nil, err
	}

	sessionId := req.SessionID
	if sessionId == "" {
		sessionId = uuid.NewString()
	}
	stateMap := req.State
	if stateMap == nil {
		stateMap = make(map[string]any)
	}
	now := xtime.Now().CurrentUnixTime()

	// fetch完整状态逻辑：
	as, err := m.fetchAppState(ctx, ident.agentID)
	if err != nil {
		return nil, err
	}
	us, err := m.fetchUserState(ctx, req.UserID, ident.agentID)
	if err != nil {
		return nil, err
	}
	appDelta, userDelta, sessDelta := extractStateDeltas(stateMap)
	// apply state delta
	if len(appDelta) > 0 {
		maps.Copy(as, appDelta)
		if err := m.upsertAppState(ctx, ident.agentID, as, now); err != nil {
			return nil, err
		}
	}
	if len(userDelta) > 0 {
		maps.Copy(us, userDelta)
		if err := m.upsertUserState(ctx, req.UserID, ident.agentID, us, now); err != nil {
			return nil, err
		}
	}
	sess := &chatSession{
		SessionID:  sessionId,
		AgentName:  ident.agentName,
		AgentID:    ident.agentID,
		UserID:     req.UserID,
		State:      sessDelta,
		CreateTime: now,
		UpdateTime: now,
	}
	err = m.createSession(ctx, sess)
	if err != nil {
		return nil, err
	}

	finalState := mergeStates(as, us, sessDelta)
	ms := &mongoSession{
		appName:   ident.appName,
		agentID:   ident.agentID,
		userID:    req.UserID,
		sessionID: sessionId,
		state:     finalState,
		updatedAt: time.Now(),
	}
	return &session.CreateResponse{Session: ms}, nil
}

// Get implements session.Service.Get
func (m *mongoSessionService) Get(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
	ident, err := parseAppIdentity(req.AppName)
	if err != nil {
		return nil, err
	}

	sess, err := m.findSessionById(ctx, ident.agentID, req.SessionID)
	if err != nil {
		if mongo.IsErrNoDocuments(err) {
			return nil, mongo.ErrNoSuchDocuments
		}
		return nil, err
	}

	// fetch完整状态逻辑：
	as, err := m.fetchAppState(ctx, ident.agentID)
	if err != nil {
		return nil, err
	}
	us, err := m.fetchUserState(ctx, req.UserID, ident.agentID)
	if err != nil {
		return nil, err
	}
	finalState := mergeStates(as, us, sess.State)
	ms := &mongoSession{
		appName:   ident.appName,
		agentID:   ident.agentID,
		userID:    req.UserID,
		sessionID: req.SessionID,
		state:     finalState,
		updatedAt: time.Unix(sess.UpdateTime, 0),
	}

	opFilter := make(map[string]any)
	opFilter["_sort"] = []string{"-create_time"}
	if req.NumRecentEvents > 0 {
		opFilter["_limit"] = int64(req.NumRecentEvents)
	} else if m.NumRecentEvents > 0 {
		opFilter["_limit"] = int64(m.NumRecentEvents)
	}
	var startTime int64
	if !req.After.IsZero() {
		startTime = req.After.Unix()
	} else if m.After > 0 {
		startTime = m.After
	}
	evs, err := m.findSessionEvents(ctx, ident.agentID, req.UserID, req.SessionID, startTime, opFilter)
	if err != nil {
		return nil, err
	}

	// We fetched in DESC order to get the most recent ones (due to LIMIT).
	// Now we reverse them to be in chronological ASC order for the response.
	for i, j := 0, len(evs)-1; i < j; i, j = i+1, j-1 {
		evs[i], evs[j] = evs[j], evs[i]
	}

	adkEvents := make([]*session.Event, 0, len(evs))
	for _, e := range evs {
		ev, err := unmarshalEventToAdkEvent(e)
		if err != nil {
			log.Error(ctx, "unmarshalEventToAdkEvent fail", log.String("error", err.Error()))
			continue
		}
		adkEvents = append(adkEvents, ev)
	}
	ms.events = adkEvents
	return &session.GetResponse{Session: ms}, nil
}

// List implements session.Service.List
func (m *mongoSessionService) List(ctx context.Context, req *session.ListRequest) (*session.ListResponse, error) {
	ident, err := parseAppIdentity(req.AppName)
	if err != nil {
		return nil, err
	}

	filter := make(map[string]any)
	filter["agent_id"] = ident.agentID
	if req.UserID != "" {
		filter["user_id"] = req.UserID
	}

	sessions, err := m.findSessions(ctx, filter, nil)
	if err != nil {
		return nil, err
	}

	// fetch完整状态逻辑：
	as, err := m.fetchAppState(ctx, ident.agentID)
	if err != nil {
		return nil, err
	}
	userStates := make(map[string]map[string]any)
	if req.UserID != "" {
		us, err := m.fetchUserState(ctx, req.UserID, ident.agentID)
		if err != nil {
			return nil, err
		}
		userStates[req.UserID] = us
	} else {
		uss, err := m.findUserStatesByAgentId(ctx, ident.agentID)
		if err != nil {
			return nil, err
		}
		for _, us := range uss {
			userStates[us.UserID] = us.State
		}
	}
	responseSessions := make([]session.Session, 0, len(sessions))
	for _, sessionItem := range sessions {
		us := userStates[sessionItem.UserID]
		finalState := mergeStates(as, us, sessionItem.State)
		ms := &mongoSession{
			appName:   ident.appName,
			agentID:   sessionItem.AgentID,
			userID:    sessionItem.UserID,
			sessionID: sessionItem.SessionID,
			state:     finalState,
			updatedAt: time.Unix(sessionItem.UpdateTime, 0),
		}
		responseSessions = append(responseSessions, ms)
	}
	return &session.ListResponse{Sessions: responseSessions}, nil
}

// Delete implements session.Service.Delete
func (m *mongoSessionService) Delete(ctx context.Context, req *session.DeleteRequest) error {
	ident, err := parseAppIdentity(req.AppName)
	if err != nil {
		return err
	}
	return m.removeSessionById(ctx, ident.agentID, req.SessionID)
}

// AppendEvent implements session.Service.AppendEvent
func (m *mongoSessionService) AppendEvent(ctx context.Context, curSession session.Session, event *session.Event) error {
	if curSession == nil {
		return nil
	}

	if event.Partial {
		return nil
	}

	ms, ok := curSession.(*mongoSession)
	if !ok {
		return fmt.Errorf("unexpected session type %T", ms)
	}

	// 1. Update in-memory session first (matches official database session order)
	if err := ms.appendEvent(event); err != nil {
		return err
	}

	// 2. Trim temp state before persisting to DB
	event = trimTempDeltaState(event)

	// 3. Persist to DB
	sessionDoc, err := m.findSessionById(ctx, ms.agentID, curSession.ID())
	if err != nil {
		return err
	}

	sessionState := sessionDoc.State
	// fetch完整状态逻辑：
	as, err := m.fetchAppState(ctx, sessionDoc.AgentID)
	if err != nil {
		return err
	}
	us, err := m.fetchUserState(ctx, sessionDoc.UserID, sessionDoc.AgentID)
	if err != nil {
		return err
	}
	appDelta, userDelta, sessDelta := extractStateDeltas(event.Actions.StateDelta)
	// apply state delta
	if len(appDelta) > 0 {
		maps.Copy(as, appDelta)
		if err = m.upsertAppState(ctx, sessionDoc.AgentID, as, event.Timestamp.Unix()); err != nil {
			return err
		}
	}
	if len(userDelta) > 0 {
		maps.Copy(us, userDelta)
		if err = m.upsertUserState(ctx, sessionDoc.UserID, sessionDoc.AgentID, us, event.Timestamp.Unix()); err != nil {
			return err
		}
	}
	if len(sessDelta) > 0 {
		maps.Copy(sessionState, sessDelta)
	}

	mEvent := marshalAdkEventToEvent(event, sessionDoc.SessionID, sessionDoc.AgentID, sessionDoc.UserID)
	err = m.createEvent(ctx, mEvent)
	if err != nil {
		return err
	}

	sessionSet := make(map[string]any)
	sessionSet["$set"] = mongo.M{
		"state":       sessionState,
		"update_time": event.Timestamp.Unix(),
	}
	err = m.editSession(ctx, sessionDoc.AgentID, sessionDoc.SessionID, sessionSet)
	if err != nil {
		return err
	}

	// 4. Update local session last update time
	ms.updatedAt = event.Timestamp
	return nil
}

// fetchAppState retrieves app-scoped state by agent_id.
func (m *mongoSessionService) fetchAppState(ctx context.Context, agentId string) (map[string]any, error) {
	as, err := m.findAppState(ctx, agentId)
	if err != nil {
		if mongo.IsErrNoDocuments(err) {
			return nil, nil
		}
		return nil, err
	}
	return as.State, nil
}

// fetchUserState retrieves user-scoped state by user_id and agent_id.
func (m *mongoSessionService) fetchUserState(ctx context.Context, userId, agentId string) (map[string]any, error) {
	us, err := m.findUserState(ctx, userId, agentId)
	if err != nil {
		if mongo.IsErrNoDocuments(err) {
			return nil, nil
		}
		return nil, err
	}
	return us.State, nil
}

// marshalAdkEventToEvent 将session.Event转为落地到Mongo的Event定义
func marshalAdkEventToEvent(e *session.Event, sessID, agentID, userID string) *event {
	return &event{
		EventID:            e.ID,
		SessionID:          sessID,
		AgentID:            agentID,
		UserID:             userID,
		InvocationID:       e.InvocationID,
		Author:             e.Author,
		Branch:             e.Branch,
		Actions:            e.Actions,
		LongRunningToolIDs: e.LongRunningToolIDs,
		Content:            e.LLMResponse.Content,
		GroundingMetadata:  e.LLMResponse.GroundingMetadata,
		CustomMetadata:     e.LLMResponse.CustomMetadata,
		UsageMetadata:      e.LLMResponse.UsageMetadata,
		CitationMetadata:   e.LLMResponse.CitationMetadata,
		Partial:            e.Partial,
		TurnComplete:       e.TurnComplete,
		Interrupted:        e.Interrupted,
		ErrorCode:          e.ErrorCode,
		ErrorMessage:       e.ErrorMessage,
		CreateTime:         e.Timestamp.Unix(),
	}
}

// unmarshalEventToAdkEvent 使用 BSON round-trip 技巧将 EventDoc 转换为 session.Event，
// 正确处理 Actions, Content, Metadata 等复杂嵌套结构。
func unmarshalEventToAdkEvent(d *event) (*session.Event, error) {
	e := &session.Event{
		ID:                 d.EventID,
		InvocationID:       d.InvocationID,
		Author:             d.Author,
		Branch:             d.Branch,
		Timestamp:          time.Unix(d.CreateTime, 0),
		LongRunningToolIDs: d.LongRunningToolIDs,
		LLMResponse: model.LLMResponse{
			Partial:      d.Partial,
			TurnComplete: d.TurnComplete,
			Interrupted:  d.Interrupted,
			ErrorCode:    d.ErrorCode,
			ErrorMessage: d.ErrorMessage,
		},
	}

	if d.Actions != nil {
		bytes, err := mongo.Marshal(d.Actions)
		if err != nil {
			return nil, fmt.Errorf("marshal actions: %w", err)
		}
		if err := mongo.Unmarshal(bytes, &e.Actions); err != nil {
			return nil, fmt.Errorf("unmarshal actions: %w", err)
		}
	}

	if d.Content != nil {
		bytes, err := mongo.Marshal(d.Content)
		if err != nil {
			return nil, fmt.Errorf("marshal content: %w", err)
		}
		var content genai.Content
		if err := mongo.Unmarshal(bytes, &content); err != nil {
			return nil, fmt.Errorf("unmarshal content: %w", err)
		}
		e.LLMResponse.Content = &content
	}

	if d.GroundingMetadata != nil {
		bytes, err := mongo.Marshal(d.GroundingMetadata)
		if err != nil {
			return nil, fmt.Errorf("marshal grounding: %w", err)
		}
		var gm genai.GroundingMetadata
		if err := mongo.Unmarshal(bytes, &gm); err != nil {
			return nil, fmt.Errorf("unmarshal grounding: %w", err)
		}
		e.LLMResponse.GroundingMetadata = &gm
	}

	if d.CustomMetadata != nil {
		bytes, err := mongo.Marshal(d.CustomMetadata)
		if err != nil {
			return nil, fmt.Errorf("marshal custom meta: %w", err)
		}
		var cm map[string]any
		if err := mongo.Unmarshal(bytes, &cm); err != nil {
			return nil, fmt.Errorf("unmarshal custom meta: %w", err)
		}
		e.LLMResponse.CustomMetadata = cm
	}

	if d.UsageMetadata != nil {
		bytes, err := mongo.Marshal(d.UsageMetadata)
		if err != nil {
			return nil, fmt.Errorf("marshal usage: %w", err)
		}
		var um genai.GenerateContentResponseUsageMetadata
		if err := mongo.Unmarshal(bytes, &um); err != nil {
			return nil, fmt.Errorf("unmarshal usage: %w", err)
		}
		e.LLMResponse.UsageMetadata = &um
	}

	if d.CitationMetadata != nil {
		bytes, err := mongo.Marshal(d.CitationMetadata)
		if err != nil {
			return nil, fmt.Errorf("marshal citation: %w", err)
		}
		var cm genai.CitationMetadata
		if err := mongo.Unmarshal(bytes, &cm); err != nil {
			return nil, fmt.Errorf("unmarshal citation: %w", err)
		}
		e.LLMResponse.CitationMetadata = &cm
	}
	return e, nil
}

// extractStateDeltas splits a single state delta map into three separate maps
// for app, user, and session states based on key prefixes.
// Temporary keys (starting with TempStatePrefix) are ignored.
func extractStateDeltas(delta map[string]any) (
	appStateDelta, userStateDelta, sessionStateDelta map[string]any,
) {
	appStateDelta = make(map[string]any)
	userStateDelta = make(map[string]any)
	sessionStateDelta = make(map[string]any)

	if delta == nil {
		return appStateDelta, userStateDelta, sessionStateDelta
	}

	for key, value := range delta {
		if cleanKey, found := strings.CutPrefix(key, session.KeyPrefixApp); found {
			appStateDelta[cleanKey] = value
		} else if cleanKey, found := strings.CutPrefix(key, session.KeyPrefixUser); found {
			userStateDelta[cleanKey] = value
		} else if !strings.HasPrefix(key, session.KeyPrefixTemp) {
			sessionStateDelta[key] = value
		}
	}
	return appStateDelta, userStateDelta, sessionStateDelta
}

// mergeStates combines app, user, and session state maps into a single map
// for client-side responses, adding the appropriate prefixes back.
func mergeStates(appState, userState, sessionState map[string]any) map[string]any {
	totalSize := len(appState) + len(userState) + len(sessionState)
	mergedState := make(map[string]any, totalSize)
	maps.Copy(mergedState, sessionState)

	for key, value := range appState {
		mergedState[session.KeyPrefixApp+key] = value
	}
	for key, value := range userState {
		mergedState[session.KeyPrefixUser+key] = value
	}
	return mergedState
}

// parseAppIdentity parses app_name in the format agent_name::agent_id.
// parseAppIdentity parses app_name in the format agent_name::agent_id.
func parseAppIdentity(appName string) (appIdentity, error) {
	if appName == "" {
		return appIdentity{}, errAppNameRequired
	}
	idx := strings.LastIndex(appName, appNameAgentSep)
	if idx <= 0 || idx+len(appNameAgentSep) >= len(appName) {
		return appIdentity{}, fmt.Errorf("invalid app_name format %s, expected agent_name%sagent_id", appName, appNameAgentSep)
	}
	return appIdentity{
		appName:   appName,
		agentName: appName[:idx],
		agentID:   appName[idx+len(appNameAgentSep):],
	}, nil
}

type appState struct {
	ID         mongo.ObjectID `bson:"_id,omitempty"`
	AgentID    string         `bson:"agent_id"`
	State      map[string]any `bson:"state"`
	UpdateTime int64          `bson:"update_time"`
}

type userState struct {
	ID         mongo.ObjectID `bson:"_id,omitempty"`
	AgentID    string         `bson:"agent_id"`
	UserID     string         `bson:"user_id"`
	State      map[string]any `bson:"state"`
	UpdateTime int64          `bson:"update_time"`
}

type chatSession struct {
	ID         mongo.ObjectID `bson:"_id,omitempty"`
	SessionID  string         `bson:"session_id"`
	AgentID    string         `bson:"agent_id"`
	AgentName  string         `bson:"agent_name"`
	UserID     string         `bson:"user_id"`
	State      map[string]any `bson:"state"`
	Deleted    int32          `bson:"deleted"`
	Title      string         `bson:"title"`
	Summarized int32          `bson:"summarized"` // 是否已完成标题总结：0=未完成 1=已完成
	CreateTime int64          `bson:"create_time"`
	UpdateTime int64          `bson:"update_time"`
}

type event struct {
	ID                 mongo.ObjectID `bson:"_id,omitempty"`
	EventID            string         `bson:"event_id"`
	SessionID          string         `bson:"session_id"`
	AgentID            string         `bson:"agent_id"`
	UserID             string         `bson:"user_id"`
	InvocationID       string         `bson:"invocation_id"`
	Author             string         `bson:"author"`
	Branch             string         `bson:"branch"`
	Actions            any            `bson:"actions"`
	LongRunningToolIDs []string       `bson:"long_running_tool_ids"`
	Content            any            `bson:"content"`
	GroundingMetadata  any            `bson:"grounding_metadata"`
	UsageMetadata      any            `bson:"usage_metadata"`
	CitationMetadata   any            `bson:"citation_metadata"`
	CustomMetadata     map[string]any `bson:"custom_metadata"`
	Partial            bool           `bson:"partial"`
	TurnComplete       bool           `bson:"turn_complete"`
	Interrupted        bool           `bson:"interrupted"`
	ErrorCode          string         `bson:"error_code"`
	ErrorMessage       string         `bson:"error_message"`
	CreateTime         int64          `bson:"create_time"`
}

const (
	collectionAppState  = "app_state"
	collectionUserState = "user_state"
	collectionSession   = "session"
	collectionEvent     = "event"
)

// createSession inserts a new session document.
func (m *mongoSessionService) createSession(ctx context.Context, cs *chatSession) (err error) {
	result, err := m.db.GetCollection(collectionSession).Insert(ctx, cs)
	if err != nil {
		log.Error(ctx, "create session fail", log.String("error", err.Error()))
		return
	}
	log.Debug(ctx, "create session success", log.Any("id", result.InsertedID))
	return
}

// editSession updates a session document by agent_id and session_id.
func (m *mongoSessionService) editSession(ctx context.Context, agentId, sessionId string, set map[string]any) (err error) {
	filter := mongo.M{
		"agent_id":   agentId,
		"session_id": sessionId,
	}

	err = m.db.GetCollection(collectionSession).UpdateOne(ctx, filter, set)
	if err != nil {
		log.Error(ctx, "edit session fail", log.String("error", err.Error()))
		return
	}
	return
}

// findSessionById retrieves a session document by agent_id and session_id.
func (m *mongoSessionService) findSessionById(ctx context.Context, agentId, sessionId string) (session *chatSession, err error) {
	session = &chatSession{}
	err = m.db.GetCollection(collectionSession).
		Find(ctx, mongo.M{"agent_id": agentId, "session_id": sessionId, "deleted": 0}).
		One(session)

	if err != nil {
		log.Error(ctx, "find session info fail", log.String("agent_id", agentId),
			log.String("session_id", sessionId), log.String("error", err.Error()))
		return
	}
	return
}

// findSessions queries session documents by filter and optional sort/limit options.
func (m *mongoSessionService) findSessions(ctx context.Context, filter map[string]any, opFilter map[string]any) (sessions []*chatSession, err error) {
	sessions = make([]*chatSession, 0)
	if _, ok := filter["deleted"]; !ok {
		filter["deleted"] = 0
	}
	query := m.db.GetCollection(collectionSession).Find(ctx, filter)
	if opFilter != nil {
		if sort, ok := opFilter["_sort"]; ok {
			for _, s := range sort.([]string) {
				query = query.Sort(s)
			}
			delete(opFilter, "_sort")
		}
		if _, ok := opFilter["_limit"]; ok {
			if limit, ok := opFilter["_limit"].(int64); ok {
				query = query.Limit(limit)
			}
			delete(opFilter, "_limit")
		}
	}

	err = query.All(&sessions)
	if err != nil {
		log.Error(ctx, "find user session info fail", log.Any("filter", filter),
			log.String("error", err.Error()))
		return
	}
	return
}

// removeSessionById soft-deletes a session document by agent_id and session_id.
func (m *mongoSessionService) removeSessionById(ctx context.Context, agentId, sessionId string) (err error) {
	filter := mongo.M{
		"agent_id":   agentId,
		"session_id": sessionId,
	}
	setCond := mongo.M{
		"$set": mongo.M{
			"deleted":     1,
			"update_time": xtime.Now().CurrentUnixTime(),
		},
	}
	err = m.db.GetCollection(collectionSession).UpdateOne(ctx, filter, setCond)
	if err != nil {
		log.Error(ctx, "remove session info", log.String("agent_id", agentId),
			log.String("session_id", sessionId), log.String("error", err.Error()))
		return
	}
	return
}

// findAppState retrieves an app_state document by agent_id.
func (m *mongoSessionService) findAppState(ctx context.Context, agentId string) (as *appState, err error) {
	as = &appState{}
	err = m.db.GetCollection(collectionAppState).
		Find(ctx, mongo.M{"agent_id": agentId}).
		One(as)
	if err != nil {
		log.Error(ctx, "find app state info fail", log.String("agent_id", agentId), log.String("error", err.Error()))
		return
	}
	return
}

// upsertAppState creates or updates an app_state document by agent_id.
func (m *mongoSessionService) upsertAppState(ctx context.Context, agentId string, state map[string]any, updateTime int64) (err error) {
	filter := mongo.M{"agent_id": agentId}
	set := mongo.M{"agent_id": agentId, "state": state, "update_time": updateTime}
	result, err := m.db.GetCollection(collectionAppState).Upsert(ctx, filter, set)
	if err != nil {
		log.Error(ctx, "upsert app state info fail", log.String("agent_id", agentId),
			log.Any("state", state), log.Int64("update_time", updateTime), log.String("error", err.Error()))
		return
	}
	log.Debug(ctx, "upsert app status success", log.Any("result", result))
	return
}

// findUserState retrieves a user_state document by user_id and agent_id.
func (m *mongoSessionService) findUserState(ctx context.Context, userId, agentId string) (us *userState, err error) {
	us = &userState{}
	err = m.db.GetCollection(collectionUserState).
		Find(ctx, mongo.M{"agent_id": agentId, "user_id": userId}).
		One(us)
	if err != nil {
		log.Error(ctx, "find user state info fail", log.String("agent_id", agentId),
			log.String("user_id", userId), log.String("error", err.Error()))
		return
	}
	return
}

// upsertUserState creates or updates a user_state document by user_id and agent_id.
func (m *mongoSessionService) upsertUserState(ctx context.Context, userId, agentId string, state map[string]any, updateTime int64) (err error) {
	filter := mongo.M{"agent_id": agentId, "user_id": userId}
	set := mongo.M{"agent_id": agentId, "user_id": userId, "state": state, "update_time": updateTime}
	result, err := m.db.GetCollection(collectionUserState).Upsert(ctx, filter, set)
	if err != nil {
		log.Error(ctx, "upsert user state info fail", log.String("agent_id", agentId),
			log.Any("state", state), log.Int64("update_time", updateTime),
			log.String("user_id", userId), log.String("error", err.Error()))
		return
	}
	log.Debug(ctx, "upsert app status success", log.Any("result", result))
	return
}

// findUserStatesByAgentId retrieves all user_state documents for an agent_id.
func (m *mongoSessionService) findUserStatesByAgentId(ctx context.Context, agentId string) (uss []*userState, err error) {
	uss = make([]*userState, 0)
	err = m.db.GetCollection(collectionUserState).
		Find(ctx, mongo.M{"agent_id": agentId}).
		All(&uss)
	if err != nil {
		log.Error(ctx, "find app all user state info fail", log.String("agent_id", agentId), log.String("error", err.Error()))
		return
	}
	return
}

// findSessionEvents retrieves event documents by agent_id, user_id and session_id with optional time and query options.
func (m *mongoSessionService) findSessionEvents(ctx context.Context, agentId, userId, sessionId string, startTime int64, opFilter map[string]any) (events []*event, err error) {
	events = make([]*event, 0)
	filter := mongo.M{"agent_id": agentId, "user_id": userId, "session_id": sessionId}
	if startTime > int64(0) {
		filter["create_time"] = mongo.M{"$gte": startTime}
	}
	query := m.db.GetCollection(collectionEvent).Find(ctx, filter)
	if sort, ok := opFilter["_sort"]; ok {
		for _, s := range sort.([]string) {
			query = query.Sort(s)
		}
		delete(opFilter, "_sort")
	}
	if _, ok := opFilter["_limit"]; ok {
		if limit, ok := opFilter["_limit"].(int64); ok {
			query = query.Limit(limit)
		}
		delete(opFilter, "_limit")
	}
	err = query.All(&events)
	if err != nil {
		log.Error(ctx, "find session event info fail", log.String("agent_id", agentId),
			log.String("user_id", userId), log.String("error", err.Error()))
		return
	}
	return
}

// createEvent inserts a new event document.
func (m *mongoSessionService) createEvent(ctx context.Context, event *event) (err error) {
	result, err := m.db.GetCollection(collectionEvent).Insert(ctx, event)
	if err != nil {
		log.Error(ctx, "create event fail", log.String("error", err.Error()))
		return
	}
	log.Debug(ctx, "create event success", log.Any("id", result.InsertedID))
	return
}

var _ session.Service = (*mongoSessionService)(nil)

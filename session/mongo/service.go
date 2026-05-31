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

// mongoSessionService implements session.Service using Mongo as the backend.
type mongoSessionService struct {
	cfg *mongo.Config
	db  *mongo.DB
}

// NewMongoSessionService creates a new Mongo-backed session service.
func NewMongoSessionService(cfg *mongo.Config) session.Service {
	db := mongo.Open(cfg)

	return &mongoSessionService{
		cfg: cfg,
		db:  db,
	}
}

// Create implements session.Service.Create
func (m *mongoSessionService) Create(ctx context.Context, req *session.CreateRequest) (*session.CreateResponse, error) {
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
	as, err := m.fetchAppState(ctx, req.AppName)
	if err != nil {
		return nil, err
	}
	us, err := m.fetchUserState(ctx, req.UserID, req.AppName)
	if err != nil {
		return nil, err
	}
	appDelta, userDelta, sessDelta := extractStateDeltas(stateMap)
	// apply state delta
	if len(appDelta) > 0 {
		maps.Copy(as, appDelta)
		if err := m.upsertAppState(ctx, req.AppName, as, now); err != nil {
			return nil, err
		}
	}
	if len(userDelta) > 0 {
		maps.Copy(us, userDelta)
		if err := m.upsertUserState(ctx, req.UserID, req.AppName, us, now); err != nil {
			return nil, err
		}
	}
	sess := &chatSession{
		SessionID:  sessionId,
		AgentName:  req.AppName,
		AgentID:    req.AppName,
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
		appName:   req.AppName,
		agentID:   req.AppName,
		userID:    req.UserID,
		sessionID: sessionId,
		state:     finalState,
		updatedAt: time.Now(),
	}
	return &session.CreateResponse{Session: ms}, nil
}

// Get implements session.Service.Get
func (m *mongoSessionService) Get(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
	sess, err := m.findSessionById(ctx, req.SessionID)
	if err != nil {
		if mongo.IsErrNoDocuments(err) {
			return nil, mongo.ErrNoSuchDocuments
		}
		return nil, err
	}

	// fetch完整状态逻辑：
	as, err := m.fetchAppState(ctx, req.AppName)
	if err != nil {
		return nil, err
	}
	us, err := m.fetchUserState(ctx, req.UserID, req.AppName)
	if err != nil {
		return nil, err
	}
	finalState := mergeStates(as, us, sess.State)
	ms := &mongoSession{
		appName:   req.AppName,
		agentID:   req.AppName,
		userID:    req.UserID,
		sessionID: req.SessionID,
		state:     finalState,
		updatedAt: time.Unix(sess.UpdateTime, 0),
	}

	opFilter := make(map[string]any)
	opFilter["_sort"] = []string{"-create_time"}
	if req.NumRecentEvents > 0 {
		opFilter["_limit"] = int64(req.NumRecentEvents)
	}
	var startTime int64
	if !req.After.IsZero() {
		startTime = req.After.Unix()
	}
	evs, err := m.findSessionEvents(ctx, req.UserID, req.SessionID, startTime, opFilter)
	if err != nil {
		return nil, err
	}

	// We fetched in DESC order to get the most recent ones (due to LIMIT).
	// Now we reverse them to be in chronological ASC order for the response.
	// Convert storage events to response events
	for i, j := 0, len(evs)-1; i < j; i, j = i+1, j-1 {
		evs[i], evs[j] = evs[j], evs[i]
	}

	adkEvents := make([]*session.Event, 0, len(evs))
	for _, e := range evs {
		ev, uerr := unmarshalEventToAdkEvent(e)
		if uerr != nil {
			log.Error(ctx, "unmarshalEventToAdkEvent fail", log.String("error", uerr.Error()))
			continue
		}
		adkEvents = append(adkEvents, ev)
	}
	ms.events = adkEvents
	return &session.GetResponse{Session: ms}, nil
}

// List implements session.Service.List
func (m *mongoSessionService) List(ctx context.Context, req *session.ListRequest) (*session.ListResponse, error) {
	filter := make(map[string]any)
	filter["agent_id"] = req.AppName
	if req.UserID != "" {
		filter["user_id"] = req.UserID
	}

	sessions, err := m.findSessions(ctx, filter, nil)
	if err != nil {
		return nil, err
	}

	// fetch完整状态逻辑：
	as, err := m.fetchAppState(ctx, req.AppName)
	if err != nil {
		return nil, err
	}
	userStates := make(map[string]map[string]any)
	if req.UserID != "" {
		us, err := m.fetchUserState(ctx, req.UserID, req.AppName)
		if err != nil {
			return nil, err
		}
		userStates[req.UserID] = us
	} else {
		uss, err := m.findUserStatesByAgentId(ctx, req.AppName)
		if err != nil {
			return nil, err
		}
		for _, us := range uss {
			userStates[us.UserID] = us.State
		}
	}
	responseSessions := make([]session.Session, 0, len(sessions))
	for _, session := range sessions {
		us := userStates[session.UserID]
		finalState := mergeStates(as, us, session.State)
		ms := &mongoSession{
			appName:   req.AppName,
			agentID:   session.AgentID,
			userID:    session.UserID,
			sessionID: session.SessionID,
			state:     finalState,
			updatedAt: time.Unix(session.UpdateTime, 0),
		}
		responseSessions = append(responseSessions, ms)
	}
	return &session.ListResponse{Sessions: responseSessions}, nil
}

// Delete implements session.Service.Delete
func (m *mongoSessionService) Delete(ctx context.Context, req *session.DeleteRequest) error {
	return m.removeSessionById(ctx, req.SessionID)
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
	session, err := m.findSessionById(ctx, curSession.ID())
	if err != nil {
		return err
	}

	sessionState := session.State
	// fetch完整状态逻辑：
	as, err := m.fetchAppState(ctx, session.AgentID)
	if err != nil {
		return err
	}
	us, err := m.fetchUserState(ctx, session.UserID, session.AgentID)
	if err != nil {
		return err
	}
	appDelta, userDelta, sessDelta := extractStateDeltas(event.Actions.StateDelta)
	// apply state delta
	if len(appDelta) > 0 {
		maps.Copy(as, appDelta)
		if err = m.upsertAppState(ctx, session.AgentID, as, event.Timestamp.Unix()); err != nil {
			return err
		}
	}
	if len(userDelta) > 0 {
		maps.Copy(us, userDelta)
		if err = m.upsertUserState(ctx, session.UserID, session.AgentID, us, event.Timestamp.Unix()); err != nil {
			return err
		}
	}
	if len(sessDelta) > 0 {
		maps.Copy(sessionState, sessDelta)
	}

	mEvent := marshalAdkEventToEvent(event, session.SessionID, session.AgentID, session.UserID)
	err = m.createEvent(ctx, mEvent)
	if err != nil {
		return err
	}

	sessionSet := make(map[string]any)
	sessionSet["$set"] = mongo.M{
		"state":       sessionState,
		"update_time": event.Timestamp.Unix(),
	}
	err = m.editSession(ctx, session.SessionID, sessionSet)
	if err != nil {
		return err
	}

	// 4. Update local session last update time
	ms.updatedAt = event.Timestamp
	return nil
}

func (m *mongoSessionService) fetchAppState(ctx context.Context, agentId string) (map[string]any, error) {
	return nil, nil

	as, err := m.findAppState(ctx, agentId)
	if err != nil {
		if mongo.IsErrNoDocuments(err) {
			return nil, nil
		}
		return nil, err
	}
	return as.State, nil
}

func (m *mongoSessionService) fetchUserState(ctx context.Context, userId, agentId string) (map[string]any, error) {
	return nil, nil

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
		EventID:      e.ID,
		SessionID:    sessID,
		AgentID:      agentID,
		UserID:       userID,
		InvocationID: e.InvocationID,
		Author:       e.Author,
		Branch:       e.Branch,
		// 直接赋值，mongo driver 会自动 marshal
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

	// 1. 处理 Actions
	if d.Actions != nil {
		bytes, err := mongo.Marshal(d.Actions)
		if err != nil {
			return nil, fmt.Errorf("marshal actions: %w", err)
		}
		if err := mongo.Unmarshal(bytes, &e.Actions); err != nil {
			return nil, fmt.Errorf("unmarshal actions: %w", err)
		}
	}

	// 2. 处理 Content (*genai.Content)
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

	// 3. 处理 GroundingMetadata
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

	// 4. 处理 CustomMetadata (map[string]any)
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

	// 5. 处理 UsageMetadata
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

	// 6. 处理 CitationMetadata
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
	// Initialize the maps to be returned.
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
			// This key belongs to the session state, as long as it's not temporary.
			sessionStateDelta[key] = value
		}
	}
	return appStateDelta, userStateDelta, sessionStateDelta
}

// mergeStates combines app, user, and session state maps into a single map
// for client-side responses, adding the appropriate prefixes back.
func mergeStates(appState, userState, sessionState map[string]any) map[string]any {
	// Pre-allocate map capacity for efficiency.
	totalSize := len(appState) + len(userState) + len(sessionState)
	mergedState := make(map[string]any, totalSize)

	// In Go, we create a new map and copy key-value pairs. This is equivalent
	// to the goal of Python's copy.deepcopy() in this context, which is to
	// avoid modifying the original sessionState map.
	maps.Copy(mergedState, sessionState)

	for key, value := range appState {
		mergedState[session.KeyPrefixApp+key] = value
	}

	for key, value := range userState {
		mergedState[session.KeyPrefixUser+key] = value
	}

	return mergedState
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
	CreateTime int64          `bson:"create_time"`
	UpdateTime int64          `bson:"update_time"`
}

type event struct {
	ID           mongo.ObjectID `bson:"_id,omitempty"`
	EventID      string         `bson:"event_id"`
	SessionID    string         `bson:"session_id"`
	AgentID      string         `bson:"agent_id"`
	UserID       string         `bson:"user_id"`
	InvocationID string         `bson:"invocation_id"`
	Author       string         `bson:"author"`
	Branch       string         `bson:"branch"`

	// 复杂对象直接存储为内嵌文档
	Actions            any      `bson:"actions"`
	LongRunningToolIDs []string `bson:"long_running_tool_ids"`

	// LLM Response 字段
	Content           any            `bson:"content"`
	GroundingMetadata any            `bson:"grounding_metadata"`
	UsageMetadata     any            `bson:"usage_metadata"`
	CitationMetadata  any            `bson:"citation_metadata"`
	CustomMetadata    map[string]any `bson:"custom_metadata"`

	Partial      bool   `bson:"partial"`
	TurnComplete bool   `bson:"turn_complete"`
	Interrupted  bool   `bson:"interrupted"`
	ErrorCode    string `bson:"error_code"`
	ErrorMessage string `bson:"error_message"`

	CreateTime int64 `bson:"create_time"`
}

const (
	collectionAppState  = "app_state"
	collectionUserState = "user_state"
	collectionSession   = "session"
	collectionEvent     = "event"
)

// CreateSession 创建会话信息
func (m *mongoSessionService) createSession(ctx context.Context, cs *chatSession) (err error) {
	result, err := m.db.GetCollection(collectionSession).Insert(ctx, cs)
	if err != nil {
		log.Error(ctx, "create session fail", log.String("error", err.Error()))
		return
	}
	log.Debug(ctx, "create session success", log.Any("id", result.InsertedID))
	return
}

// EditSession 根据会话ID更新会话信息
func (m *mongoSessionService) editSession(ctx context.Context, sessionId string, set map[string]any) (err error) {
	filter := mongo.M{
		"session_id": sessionId,
	}

	err = m.db.GetCollection(collectionSession).UpdateOne(ctx, filter, set)
	if err != nil {
		log.Error(ctx, "edit session fail", log.String("error", err.Error()))
		return
	}
	return
}

// FindSessionById 通过id查询会话信息
func (m *mongoSessionService) findSessionById(ctx context.Context, sessionId string) (session *chatSession, err error) {
	session = &chatSession{}
	err = m.db.GetCollection(collectionSession).
		Find(ctx, mongo.M{"session_id": sessionId, "deleted": 0}).
		One(session)

	if err != nil {
		log.Error(ctx, "find session info fail", log.String("session_id", sessionId),
			log.String("error", err.Error()))
		return
	}
	return
}

// FindSessionsByUserId 查询用户会话信息
func (m *mongoSessionService) findSessionsByUserId(ctx context.Context, userId string, opFilter map[string]any) (sessions []*chatSession, err error) {
	sessions = make([]*chatSession, 0)

	query := m.db.GetCollection(collectionSession).
		Find(ctx, mongo.M{"user_id": userId, "deleted": 0})
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

	err = query.All(&sessions)
	if err != nil {
		log.Error(ctx, "find user session info fail", log.String("user_id", userId),
			log.String("error", err.Error()))
		return
	}
	return
}

// FindSessions 查询会话信息列表
func (m *mongoSessionService) findSessions(ctx context.Context, filter map[string]any, opFilter map[string]any) (sessions []*chatSession, err error) {
	sessions = make([]*chatSession, 0)

	if _, ok := filter["deleted"]; !ok {
		filter["deleted"] = 0
	}

	query := m.db.GetCollection(collectionSession).Find(ctx, filter)
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

	err = query.All(&sessions)
	if err != nil {
		log.Error(ctx, "find user session info fail", log.Any("filter", filter),
			log.String("error", err.Error()))
		return
	}
	return
}

// RemoveSessionById 通过id删除会话信息
func (m *mongoSessionService) removeSessionById(ctx context.Context, sessionId string) (err error) {
	filter := mongo.M{
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
		log.Error(ctx, "remove session info", log.String("session_id", sessionId),
			log.String("error", err.Error()))
		return
	}
	return
}

// CreateAppState 创建应用级状态信息
func (m *mongoSessionService) createAppState(ctx context.Context, as *appState) (err error) {
	result, err := m.db.GetCollection(collectionAppState).Insert(ctx, as)
	if err != nil {
		log.Error(ctx, "create app state fail", log.String("error", err.Error()))
		return
	}
	log.Debug(ctx, "create app state success", log.Any("id", result.InsertedID))
	return
}

// FindAppState 通过AgentID查询应用级状态信息
func (m *mongoSessionService) findAppState(ctx context.Context, agentId string) (as *appState, err error) {
	as = &appState{}
	err = m.db.GetCollection(collectionAppState).
		Find(ctx, mongo.M{"agent_id": agentId}).
		One(as)

	if err != nil {
		log.Error(ctx, "find app state info fail", log.String("agent_id", agentId),
			log.String("error", err.Error()))
		return
	}
	return
}

// EditAppState 通过AgentID更新应用级状态信息
func (m *mongoSessionService) editAppState(ctx context.Context, agentId string, state map[string]any) (err error) {
	filter := mongo.M{
		"agent_id": agentId,
	}
	set := mongo.M{
		"$set": mongo.M{
			"state":       state,
			"update_time": xtime.Now().CurrentUnixTime(),
		},
	}
	err = m.db.GetCollection(collectionAppState).UpdateOne(ctx, filter, set)
	if err != nil {
		log.Error(ctx, "edit app state info fail", log.String("agent_id", agentId),
			log.String("error", err.Error()))
		return
	}
	return
}

// UpsertAppState 通过AgentID创建或更新应用级状态信息
func (m *mongoSessionService) upsertAppState(ctx context.Context, agentId string, state map[string]any, updateTime int64) (err error) {
	filter := mongo.M{
		"agent_id": agentId,
	}
	set := mongo.M{
		"agent_id":    agentId,
		"state":       state,
		"update_time": updateTime,
	}
	result, err := m.db.GetCollection(collectionAppState).Upsert(ctx, filter, set)
	if err != nil {
		log.Error(ctx, "upsert app state info fail", log.String("agent_id", agentId),
			log.Any("state", state), log.Int64("update_time", updateTime),
			log.String("error", err.Error()))
		return
	}
	log.Debug(ctx, "upsert app status success", log.Any("result", result))
	return
}

// CreateUserState 创建用户级状态信息
func (m *mongoSessionService) createUserState(ctx context.Context, as *userState) (err error) {
	result, err := m.db.GetCollection(collectionUserState).Insert(ctx, as)
	if err != nil {
		log.Error(ctx, "create user state fail", log.String("error", err.Error()))
		return
	}
	log.Debug(ctx, "create user state success", log.Any("id", result.InsertedID))
	return
}

// FindUserState 通过UserID及AgentId查询用户级状态信息
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

// EditUserState 通过UserID及AgentId更新用户级状态信息
func (m *mongoSessionService) editUserState(ctx context.Context, userId, agentId string, state map[string]any) (err error) {
	filter := mongo.M{
		"agent_id": agentId,
		"user_id":  userId,
	}
	set := mongo.M{
		"$set": mongo.M{
			"state":       state,
			"update_time": xtime.Now().CurrentUnixTime(),
		},
	}
	err = m.db.GetCollection(collectionUserState).UpdateOne(ctx, filter, set)
	if err != nil {
		log.Error(ctx, "edit user state info fail", log.String("agent_id", agentId),
			log.String("user_id", userId), log.String("error", err.Error()))
		return
	}
	return
}

// UpsertUserState 通过UserID及AgentId创建或更新用户级状态信息
func (m *mongoSessionService) upsertUserState(ctx context.Context, userId, agentId string, state map[string]any, updateTime int64) (err error) {
	filter := mongo.M{
		"agent_id": agentId,
		"user_id":  userId,
	}
	set := mongo.M{
		"agent_id":    agentId,
		"user_id":     userId,
		"state":       state,
		"update_time": updateTime,
	}
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

// FindUserStatesByAgentId 通过AgentId查询所有用户状态
func (m *mongoSessionService) findUserStatesByAgentId(ctx context.Context, agentId string) (uss []*userState, err error) {
	uss = make([]*userState, 0)
	err = m.db.GetCollection(collectionUserState).
		Find(ctx, mongo.M{"agent_id": agentId}).
		All(&uss)

	if err != nil {
		log.Error(ctx, "find app all user state info fail", log.String("agent_id", agentId),
			log.String("error", err.Error()))
		return
	}
	return
}

// FindSessionEvents 查询用户会话事件列表
func (m *mongoSessionService) findSessionEvents(ctx context.Context, userId, sessionId string, startTime int64, opFilter map[string]any) (events []*event, err error) {
	events = make([]*event, 0)

	filter := mongo.M{
		"user_id":    userId,
		"session_id": sessionId,
	}
	if startTime > int64(0) {
		filter["create_time"] = mongo.M{
			"$gte": startTime,
		}
	}
	query := m.db.GetCollection(collectionEvent).
		Find(ctx, filter)

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
		log.Error(ctx, "find session event info fail", log.String("user_id", userId),
			log.String("error", err.Error()))
		return
	}
	return
}

// CreateEvent 创建事件信息
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

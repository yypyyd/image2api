package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"backend/internal/model"
	"backend/internal/repo"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ErrNotFound is returned by delete/adjust service methods when the target
// row does not exist, so handlers can translate it into a 404 (GORM's Delete
// does not error on a zero-row delete).
var ErrNotFound = errors.New("not found")
var ErrModelAliasCollision = errors.New("model alias collision")

type AdminWriteService struct {
	users    *repo.UserRepository
	showcase *repo.ShowcaseRepository
	models   *repo.ModelRepository
	events   *repo.EventRepository
	apiKeys  *repo.APIKeyRepository
	tokens   *repo.TokenRepository
	orders   *repo.OrderRepository
}

func NewAdminWriteService(users *repo.UserRepository, showcase *repo.ShowcaseRepository, models *repo.ModelRepository, events *repo.EventRepository, apiKeys *repo.APIKeyRepository, tokens *repo.TokenRepository, orders *repo.OrderRepository) *AdminWriteService {
	return &AdminWriteService{
		users:    users,
		showcase: showcase,
		models:   models,
		events:   events,
		apiKeys:  apiKeys,
		tokens:   tokens,
		orders:   orders,
	}
}

func (s *AdminWriteService) CreateUser(ctx context.Context, body map[string]any) (*model.User, error) {
	email, err := ValidateEmail(stringValue(body["email"]))
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(stringValue(body["name"]))
	if name != "" {
		name, err = ValidateUsername(name)
		if err != nil {
			return nil, err
		}
	}
	password := stringValue(body["password"])
	role := normalizedRole(stringValue(body["role"]))
	// 管理员唯一:不能通过用户管理创建新的 admin(只能是 user / agent)。
	if role == "admin" {
		role = "user"
	}
	status := normalizedStatus(stringValue(body["status"]))
	credits := maxFloat(0, floatValue(body["credits"]))
	notes := strings.TrimSpace(stringValue(body["notes"]))
	cgroupID := strings.TrimSpace(stringValue(body["concurrency_group_id"]))

	exists, err := s.users.ExistsEmail(ctx, email, "")
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.New("邮箱已存在")
	}
	if name != "" {
		exists, err = s.users.ExistsName(ctx, name, "")
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, errors.New("用户名已存在")
		}
	}

	passwordHash := ""
	if strings.TrimSpace(password) != "" {
		if err := ValidatePassword(password); err != nil {
			return nil, err
		}
		h, err := HashPassword(password)
		if err != nil {
			return nil, err
		}
		passwordHash = h
	}

	user := &model.User{
		ID:                 "u-" + uuid.NewString()[:10],
		Email:              email,
		Name:               name,
		PasswordHash:       passwordHash,
		Role:               role,
		Status:             status,
		Credits:            credits,
		Notes:              notes,
		ConcurrencyGroupID: cgroupID,
		InviteCode:         randomInviteCode(),
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	if err := s.users.Create(ctx, user); err != nil {
		return nil, err
	}
	return s.users.GetByID(ctx, user.ID)
}

func (s *AdminWriteService) UpdateUser(ctx context.Context, userID string, body map[string]any) (*model.User, error) {
	patch := map[string]any{}
	if _, ok := body["email"]; ok {
		email, err := ValidateEmail(stringValue(body["email"]))
		if err != nil {
			return nil, err
		}
		exists, err := s.users.ExistsEmail(ctx, email, userID)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, errors.New("邮箱已存在")
		}
		patch["email"] = email
	}
	if _, ok := body["name"]; ok {
		name := strings.TrimSpace(stringValue(body["name"]))
		if name != "" {
			var err error
			name, err = ValidateUsername(name)
			if err != nil {
				return nil, err
			}
			exists, err := s.users.ExistsName(ctx, name, userID)
			if err != nil {
				return nil, err
			}
			if exists {
				return nil, errors.New("用户名已存在")
			}
		}
		patch["name"] = name
	}
	if _, ok := body["role"]; ok {
		newRole := normalizedRole(stringValue(body["role"]))
		// 管理员唯一:不能把任何人提升为 admin;也绝不改动现有 admin 的角色
		// (防止把唯一管理员误降级导致后台失去管理员)。
		cur, _ := s.users.GetByID(ctx, userID)
		if newRole != "admin" && (cur == nil || cur.Role != "admin") {
			patch["role"] = newRole
		}
	}
	if _, ok := body["status"]; ok {
		patch["status"] = normalizedStatus(stringValue(body["status"]))
	}
	if _, ok := body["credits"]; ok {
		patch["credits"] = maxFloat(0, floatValue(body["credits"]))
	}
	if _, ok := body["notes"]; ok {
		patch["notes"] = strings.TrimSpace(stringValue(body["notes"]))
	}
	if _, ok := body["concurrency_group_id"]; ok {
		patch["concurrency_group_id"] = strings.TrimSpace(stringValue(body["concurrency_group_id"]))
	}
	if _, ok := body["password"]; ok && strings.TrimSpace(stringValue(body["password"])) != "" {
		if err := ValidatePassword(stringValue(body["password"])); err != nil {
			return nil, err
		}
		h, err := HashPassword(stringValue(body["password"]))
		if err != nil {
			return nil, err
		}
		patch["password_hash"] = h
	}
	return s.users.Update(ctx, userID, patch)
}

func (s *AdminWriteService) DeleteUser(ctx context.Context, userID string) error {
	rows, err := s.users.Delete(ctx, userID)
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteUsers removes many users in one call (multi-select). Returns the count
// removed.
func (s *AdminWriteService) DeleteUsers(ctx context.Context, ids []string) (int, error) {
	seen := make(map[string]struct{}, len(ids))
	clean := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		clean = append(clean, id)
	}
	if len(clean) == 0 {
		return 0, nil
	}
	rows, err := s.users.DeleteByIDs(ctx, clean)
	return int(rows), err
}

func (s *AdminWriteService) AdjustUserCredits(ctx context.Context, userID string, delta float64) (*model.User, error) {
	user, err := s.users.AdjustCredits(ctx, userID, delta)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if delta != 0 {
		RecordCreditOrder(ctx, s.orders, userID, delta, "admin", "管理员调整余额")
	}
	return user, nil
}

// SetUserCredits sets a user's credit balance to an absolute value (non-negative).
// Mirrors Python users_store.adjust_credits set_to mode; the update runs inside a
// transaction with a row lock so concurrent adjustments stay consistent.
func (s *AdminWriteService) SetUserCredits(ctx context.Context, userID string, value float64) (*model.User, error) {
	if value < 0 {
		value = 0
	}
	before, err := s.users.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	user, err := s.users.SetCredits(ctx, userID, value)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if delta := user.Credits - before.Credits; delta != 0 {
		RecordCreditOrder(ctx, s.orders, userID, delta, "admin", "管理员设置余额")
	}
	return user, nil
}

func (s *AdminWriteService) CreateUserAPIKey(ctx context.Context, userID, name string) (*model.APIKey, string, error) {
	plain, err := generatePlainAPIKey()
	if err != nil {
		return nil, "", err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "admin"
	}
	key := &model.APIKey{
		ID:         "k-" + time.Now().Format("150405") + randomSuffix(2),
		UserID:     userID,
		Name:       name,
		KeyPreview: previewAPIKey(plain),
		KeyHash:    hashAPIKey(plain),
		CreatedAt:  time.Now(),
	}
	if err := s.apiKeys.Create(ctx, key); err != nil {
		return nil, "", err
	}
	return key, plain, nil
}

func (s *AdminWriteService) DeleteUserAPIKey(ctx context.Context, userID, keyID string) error {
	if strings.TrimSpace(keyID) == "" {
		return errors.New("key id required")
	}
	return s.apiKeys.DeleteByID(ctx, userID, keyID)
}

func (s *AdminWriteService) CreateShowcase(ctx context.Context, body map[string]any) (*model.ShowcaseItem, error) {
	kind := normalizedShowcaseKind(stringValue(body["kind"]))
	if kind == "" {
		return nil, errors.New("kind must be hero, bento or work")
	}
	image := strings.TrimSpace(stringValue(body["image"]))
	if image == "" {
		return nil, errors.New("请选择底图")
	}
	title := strings.TrimSpace(stringValue(body["title"]))
	prompt := strings.TrimSpace(stringValue(body["prompt"]))
	if kind != "work" {
		if title == "" {
			return nil, errors.New("请填写标题")
		}
		if prompt == "" {
			return nil, errors.New("请填写提示词")
		}
	}

	item := &model.ShowcaseItem{
		ID:        "sc-" + uuid.NewString()[:10],
		Kind:      kind,
		Title:     title,
		Subtitle:  strings.TrimSpace(stringValue(body["subtitle"])),
		Prompt:    prompt,
		Gradient:  strings.TrimSpace(stringValue(body["gradient"])),
		Span:      strings.TrimSpace(stringValue(body["span"])),
		Image:     image,
		Weight:    intValue(body["weight"]),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := s.showcase.Create(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *AdminWriteService) UpdateShowcase(ctx context.Context, entryID string, body map[string]any) (*model.ShowcaseItem, error) {
	patch := map[string]any{}
	if _, ok := body["kind"]; ok {
		kind := normalizedShowcaseKind(stringValue(body["kind"]))
		if kind == "" {
			return nil, errors.New("kind must be hero, bento or work")
		}
		patch["kind"] = kind
	}
	for _, field := range []string{"title", "subtitle", "prompt", "gradient", "span", "image"} {
		if _, ok := body[field]; ok {
			patch[field] = strings.TrimSpace(stringValue(body[field]))
		}
	}
	if _, ok := body["weight"]; ok {
		patch["weight"] = intValue(body["weight"])
	}
	return s.showcase.Update(ctx, entryID, patch)
}

func (s *AdminWriteService) DeleteShowcase(ctx context.Context, entryID string) error {
	rows, err := s.showcase.Delete(ctx, entryID)
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *AdminWriteService) CreateModel(ctx context.Context, body map[string]any) (*model.ModelConfig, error) {
	modelID := strings.TrimSpace(stringValue(body["id"]))
	modelType := normalizedModelType(stringValue(body["type"]))
	provider := strings.TrimSpace(stringValue(body["provider"]))
	alias := strings.TrimSpace(stringValue(body["alias"]))
	if modelID == "" {
		return nil, errors.New("id required")
	}
	if modelType == "" {
		return nil, errors.New("type must be image, video, or text")
	}
	if provider == "" {
		return nil, errors.New("provider required")
	}
	if err := s.validateModelNameSpace(ctx, "", modelID, alias); err != nil {
		return nil, err
	}

	prices := jsonMap(body["prices"])
	// image: tiers derive from the price keys (form omits resolutions);
	// video: resolutions come straight from the form (720p/1080p…). Python parity.
	resolutions := jsonArray(body["resolutions"])
	if modelType == "image" {
		resolutions = resolutionsFromPrices(prices)
	} else if modelType == "text" {
		resolutions = jsonArray(nil)
	}

	item := &model.ModelConfig{
		ID:                  modelID,
		Type:                modelType,
		Name:                defaultString(strings.TrimSpace(stringValue(body["name"])), modelID),
		Alias:               alias,
		Provider:            provider,
		Enabled:             boolValueWithDefault(body["enabled"], true),
		Ratios:              jsonArray(body["ratios"]),
		Prices:              prices,
		Resolutions:         resolutions,
		ImageToImage:        boolValueWithDefault(body["image_to_image"], false),
		DurationPrices:      jsonMap(body["duration_prices"]),
		PricesAgent:         jsonMap(body["prices_agent"]),
		DurationPricesAgent: jsonMap(body["duration_prices_agent"]),
		Durations:           jsonArray(body["durations"]),
		MaxReferenceImages:  intValue(body["max_reference_images"]),
		MaxReferenceVideos:  intValue(body["max_reference_videos"]),
		MaxReferenceAudios:  intValue(body["max_reference_audios"]),
		MaxReferenceMedia:   intValue(body["max_reference_media"]),
		SupportsAudioOutput: boolValueWithDefault(body["supports_audio_output"], false),
		ReferenceMode:       defaultString(strings.TrimSpace(stringValue(body["reference_mode"])), "none"),
		Weight:              intValue(body["weight"]),
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}
	if err := s.models.Create(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *AdminWriteService) UpdateModel(ctx context.Context, modelID string, body map[string]any) (*model.ModelConfig, error) {
	patch := map[string]any{}
	alias := ""
	if _, ok := body["type"]; ok {
		modelType := normalizedModelType(stringValue(body["type"]))
		if modelType == "" {
			return nil, errors.New("type must be image, video, or text")
		}
		patch["type"] = modelType
	}
	if _, ok := body["name"]; ok {
		patch["name"] = strings.TrimSpace(stringValue(body["name"]))
	}
	if _, ok := body["alias"]; ok {
		alias = strings.TrimSpace(stringValue(body["alias"]))
		if err := s.validateModelNameSpace(ctx, modelID, modelID, alias); err != nil {
			return nil, err
		}
		patch["alias"] = alias
	}
	if _, ok := body["provider"]; ok {
		provider := strings.TrimSpace(stringValue(body["provider"]))
		if provider == "" {
			return nil, errors.New("provider required")
		}
		patch["provider"] = provider
	}
	// Only touch `enabled` when the caller explicitly sends a non-null value;
	// mirrors Python models_store.update ("enabled" in fields and is not None).
	// Without this guard a PATCH that omits the field would default it to false
	// and silently disable the model.
	if raw, ok := body["enabled"]; ok && raw != nil {
		patch["enabled"] = boolValueWithDefault(raw, true)
	}
	if _, ok := body["ratios"]; ok {
		patch["ratios"] = jsonArray(body["ratios"])
	}
	if _, ok := body["prices"]; ok {
		prices := jsonMap(body["prices"])
		patch["prices"] = prices
		// Python parity (models_store.update): recompute resolutions from the new
		// price keys. An explicit `resolutions` field below (video) overrides this.
		patch["resolutions"] = resolutionsFromPrices(prices)
	}
	if _, ok := body["resolutions"]; ok {
		patch["resolutions"] = jsonArray(body["resolutions"])
	}
	if _, ok := body["image_to_image"]; ok {
		patch["image_to_image"] = boolValueWithDefault(body["image_to_image"], false)
	}
	if _, ok := body["duration_prices"]; ok {
		patch["duration_prices"] = jsonMap(body["duration_prices"])
	}
	if _, ok := body["prices_agent"]; ok {
		patch["prices_agent"] = jsonMap(body["prices_agent"])
	}
	if _, ok := body["duration_prices_agent"]; ok {
		patch["duration_prices_agent"] = jsonMap(body["duration_prices_agent"])
	}
	if _, ok := body["durations"]; ok {
		patch["durations"] = jsonArray(body["durations"])
	}
	if _, ok := body["max_reference_images"]; ok {
		patch["max_reference_images"] = intValue(body["max_reference_images"])
	}
	if _, ok := body["max_reference_videos"]; ok {
		patch["max_reference_videos"] = intValue(body["max_reference_videos"])
	}
	if _, ok := body["max_reference_audios"]; ok {
		patch["max_reference_audios"] = intValue(body["max_reference_audios"])
	}
	if _, ok := body["max_reference_media"]; ok {
		patch["max_reference_media"] = intValue(body["max_reference_media"])
	}
	if _, ok := body["supports_audio_output"]; ok {
		patch["supports_audio_output"] = boolValueWithDefault(body["supports_audio_output"], false)
	}
	if _, ok := body["reference_mode"]; ok {
		patch["reference_mode"] = defaultString(strings.TrimSpace(stringValue(body["reference_mode"])), "none")
	}
	if _, ok := body["weight"]; ok {
		patch["weight"] = intValue(body["weight"])
	}
	return s.models.Update(ctx, modelID, patch)
}

func (s *AdminWriteService) validateModelNameSpace(ctx context.Context, selfID, candidateID, candidateAlias string) error {
	candidateID = strings.TrimSpace(candidateID)
	candidateAlias = strings.TrimSpace(candidateAlias)
	if candidateID == "" && candidateAlias == "" {
		return nil
	}
	items, err := s.models.List(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ID == selfID {
			continue
		}
		existingAlias := strings.TrimSpace(item.Alias)
		if candidateID != "" && existingAlias == candidateID {
			return fmt.Errorf("%w: model id %q collides with existing alias %q", ErrModelAliasCollision, candidateID, existingAlias)
		}
		if candidateAlias != "" {
			if item.ID == candidateAlias {
				return fmt.Errorf("%w: alias %q collides with existing model id %q", ErrModelAliasCollision, candidateAlias, item.ID)
			}
			if existingAlias != "" && existingAlias == candidateAlias {
				return fmt.Errorf("%w: alias %q collides with existing alias %q", ErrModelAliasCollision, candidateAlias, existingAlias)
			}
		}
	}
	return nil
}

func (s *AdminWriteService) DeleteModel(ctx context.Context, modelID string) error {
	rows, err := s.models.Delete(ctx, modelID)
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	// Clean up: strip this model id from every custom upstream account's
	// supported-models list so no orphan reference is left behind.
	s.removeModelFromUpstreams(ctx, modelID)
	return nil
}

// removeModelFromUpstreams drops modelID from the CSV in each custom account's
// Meta["models"]. Best-effort — a failure here doesn't undo the model delete.
func (s *AdminWriteService) removeModelFromUpstreams(ctx context.Context, modelID string) {
	if s.tokens == nil {
		return
	}
	items, err := s.tokens.ListByPool(ctx, "custom")
	if err != nil {
		return
	}
	for _, it := range items {
		raw, _ := it.Meta["models"].(string)
		if strings.TrimSpace(raw) == "" {
			continue
		}
		kept := make([]string, 0, len(strings.Split(raw, ",")))
		changed := false
		for _, p := range strings.Split(raw, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if p == modelID {
				changed = true
				continue
			}
			kept = append(kept, p)
		}
		if !changed {
			continue
		}
		meta := datatypes.JSONMap{}
		for k, v := range it.Meta {
			meta[k] = v
		}
		meta["models"] = strings.Join(kept, ",")
		_, _ = s.tokens.Update(ctx, "custom", it.ID, map[string]any{"meta": meta})
	}
}

func (s *AdminWriteService) ClearLogs(ctx context.Context) (int64, error) {
	return s.events.DeleteAll(ctx)
}

func (s *AdminWriteService) ClearPendingLogs(ctx context.Context) (int64, error) {
	return s.events.DeletePending(ctx)
}

func HashPassword(password string) (string, error) {
	hash, err := GeneratePasswordHash(password)
	if err != nil {
		return "", err
	}
	return "bcrypt$" + hash, nil
}

func normalizedRole(role string) string {
	switch strings.TrimSpace(role) {
	case "admin":
		return "admin"
	case "agent":
		return "agent"
	default:
		return "user"
	}
}

func normalizedStatus(status string) string {
	if strings.TrimSpace(status) == "disabled" {
		return "disabled"
	}
	return "active"
}

func normalizedShowcaseKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case "hero", "bento", "work":
		return strings.TrimSpace(kind)
	default:
		return ""
	}
}

func normalizedModelType(v string) string {
	switch strings.TrimSpace(v) {
	case "image", "video", "text":
		return strings.TrimSpace(v)
	default:
		return ""
	}
}

func stringValue(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	default:
		return fmt.Sprint(v)
	}
}

func floatValue(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		var f float64
		_, _ = fmt.Sscanf(strings.TrimSpace(x), "%f", &f)
		return f
	default:
		return 0
	}
}

func intValue(v any) int {
	return int(floatValue(v))
}

func boolValueWithDefault(v any, fallback bool) bool {
	if v == nil {
		return fallback
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return fallback
}

// resolutionsFromPrices mirrors Python models_store._resolutions_from_prices:
// an image model's quality tiers ARE its price keys (the admin form never sends
// `resolutions` for images), returned in canonical 1K/2K/4K order. gpt-image-2,
// for example, only ever has a "1K" price, so it resolves to exactly ["1K"].
func resolutionsFromPrices(prices datatypes.JSONMap) datatypes.JSON {
	out := []string{}
	for _, r := range []string{"1K", "2K", "4K"} {
		if _, ok := prices[r]; ok {
			out = append(out, r)
		}
	}
	return jsonArray(out)
}

func jsonArray(v any) datatypes.JSON {
	if v == nil {
		return datatypes.JSON([]byte("[]"))
	}
	b, err := json.Marshal(v)
	if err != nil {
		return datatypes.JSON([]byte("[]"))
	}
	return datatypes.JSON(b)
}

func jsonMap(v any) datatypes.JSONMap {
	if v == nil {
		return datatypes.JSONMap{}
	}
	switch m := v.(type) {
	case map[string]any:
		return datatypes.JSONMap(m)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return datatypes.JSONMap{}
		}
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			return datatypes.JSONMap{}
		}
		return datatypes.JSONMap(out)
	}
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func maxFloat(min, v float64) float64 {
	if v < min {
		return min
	}
	return v
}

func randomInviteCode() string {
	return randomUpper(8)
}

var _ = gorm.ErrRecordNotFound

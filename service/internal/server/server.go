package server

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/berjistech/berjis-ecosystem/schools/service/internal/auth"
	coreauth "github.com/berjistech/berjis-ecosystem/shared/coreauth"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/csrf"
	"github.com/gofiber/fiber/v2/middleware/helmet"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/jung-kurt/gofpdf"
	"github.com/redis/go-redis/v9"
	stripe "github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/checkout/session"
	"github.com/stripe/stripe-go/v76/refund"
	"github.com/stripe/stripe-go/v76/webhook"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Options struct {
	AllowedOrigins   string
	CoreAPIBase      string
	DB               *sqlx.DB
	SMTPHost         string
	SMTPPort         string
	SMTPUsername     string
	SMTPPassword     string
	SMTPFrom         string
	SMSProvider      string
	TwilioAccountSID string
	TwilioAuthToken  string
	TwilioFrom       string
	VAPIDPublicKey   string
	VAPIDPrivateKey  string
}

func New(opts Options) *fiber.App {
	httpClientAuth := &http.Client{Timeout: 5 * time.Second}
	coreAPIBase := strings.TrimSpace(opts.CoreAPIBase)
	var authVerifier *coreauth.Verifier
	if coreAPIBase != "" {
		if v, err := coreauth.NewVerifier(coreauth.Config{
			CoreAPIBase: coreAPIBase,
			HTTPClient:  httpClientAuth,
		}); err != nil {
			fmt.Printf("warn: coreauth verifier init failed: %v\n", err)
		} else {
			authVerifier = v
		}
	}
	// Helper: evaluate certificate rules for a user in a class and issue if eligible
	evaluateCerts := func(classID, userID string) {
		if opts.DB == nil || classID == "" || userID == "" {
			return
		}
		var schoolID *string
		_ = opts.DB.Get(&schoolID, `SELECT school_id FROM classes WHERE id=$1`, classID)
		type rule struct {
			ID         string `db:"id"`
			Scope      string `db:"scope"`
			TemplateID string `db:"template_id"`
			Conditions []byte `db:"conditions"`
		}
		rules := []rule{}
		if schoolID != nil && *schoolID != "" {
			_ = opts.DB.Select(&rules, `SELECT id, scope, template_id, COALESCE(conditions,'{}'::jsonb) AS conditions
                FROM certificate_rules WHERE enabled=true AND ((scope='class' AND class_id=$1) OR (scope='school' AND school_id=$2))`, classID, *schoolID)
		} else {
			_ = opts.DB.Select(&rules, `SELECT id, scope, template_id, COALESCE(conditions,'{}'::jsonb) AS conditions
                FROM certificate_rules WHERE enabled=true AND (scope='class' AND class_id=$1)`, classID)
		}
		if len(rules) == 0 {
			return
		}
		var max float64
		{
			type trow struct {
				ID string `db:"id"`
			}
			tests := []trow{}
			_ = opts.DB.Select(&tests, `SELECT t.id FROM tests t
                LEFT JOIN subjects s ON s.id=t.subject_id
                LEFT JOIN lessons l ON l.id=t.lesson_id
                LEFT JOIN subjects s2 ON s2.id=l.subject_id
                LEFT JOIN classes c ON c.id=s.class_id
                LEFT JOIN classes c2 ON c2.id=s2.class_id
                WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted'`, classID)
			for _, tr := range tests {
				var m float64
				_ = opts.DB.Get(&m, `SELECT COALESCE(SUM(points),0) FROM test_questions WHERE test_id=$1`, tr.ID)
				max += m
			}
		}
		earned := 0.0
		if max > 0 {
			type arow struct {
				TestID  string   `db:"test_id"`
				Score   *float64 `db:"score"`
				Grading []byte   `db:"grading"`
			}
			attempts := []arow{}
			_ = opts.DB.Select(&attempts, `SELECT ta.test_id, ta.score, COALESCE(ta.grading,'null'::jsonb) AS grading FROM test_attempts ta
               WHERE ta.student_user_id=$1 AND ta.test_id IN (
                    SELECT t.id FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$2 OR c2.id=$2) AND t.status<>'deleted'
               )`, userID, classID)
			for _, a := range attempts {
				var m float64
				_ = opts.DB.Get(&m, `SELECT COALESCE(SUM(points),0) FROM test_questions WHERE test_id=$1`, a.TestID)
				if len(a.Grading) > 0 && string(a.Grading) != "null" {
					var g map[string]any
					_ = json.Unmarshal(a.Grading, &g)
					if v, ok := g["earned"].(float64); ok {
						earned += v
						continue
					}
				}
				if a.Score != nil {
					earned += (*a.Score / 100.0) * m
				}
			}
		}
		percent := 0.0
		if max > 0 {
			percent = (earned / max) * 100.0
		}
		var totalTests int
		_ = opts.DB.Get(&totalTests, `SELECT COUNT(*) FROM (
            SELECT t.id FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted') q`, classID)
		var completedTests int
		_ = opts.DB.Get(&completedTests, `SELECT COUNT(*) FROM test_attempts WHERE student_user_id=$1 AND test_id IN (
            SELECT t.id FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$2 OR c2.id=$2) AND t.status<>'deleted') AND status<>'in_progress'`, userID, classID)
		var totalLessons int
		_ = opts.DB.Get(&totalLessons, `SELECT COUNT(*) FROM lessons l JOIN subjects s ON s.id=l.subject_id WHERE s.class_id=$1 AND l.status='active'`, classID)
		var lessonsCompleted int
		_ = opts.DB.Get(&lessonsCompleted, `SELECT COUNT(*) FROM lesson_progress WHERE student_user_id=$1 AND lesson_id IN (SELECT l.id FROM lessons l JOIN subjects s ON s.id=l.subject_id WHERE s.class_id=$2) AND status='completed'`, userID, classID)
		for _, r := range rules {
			var cond struct {
				MinPercent               float64 `json:"minPercent"`
				RequireAllTestsCompleted bool    `json:"requireAllTestsCompleted"`
				MinLessonsCompleted      int     `json:"minLessonsCompleted"`
			}
			_ = json.Unmarshal(r.Conditions, &cond)
			if cond.MinPercent > 0 && percent+1e-9 < cond.MinPercent {
				continue
			}
			if cond.RequireAllTestsCompleted && totalTests > 0 && completedTests < totalTests {
				continue
			}
			if cond.MinLessonsCompleted > 0 && lessonsCompleted < cond.MinLessonsCompleted {
				continue
			}
			var exists bool
			_ = opts.DB.Get(&exists, `SELECT EXISTS (SELECT 1 FROM certificates WHERE template_id=$1 AND recipient_user_id=$2 AND class_id=$3 AND status='issued')`, r.TemplateID, userID, classID)
			if exists {
				continue
			}
			code := strings.ToUpper(strings.ReplaceAll(uuid.New().String(), "-", ""))
			if len(code) > 12 {
				code = code[:12]
			}
			verifyURL := strings.TrimRight(opts.CoreAPIBase, "/")
			_, _ = opts.DB.Exec(`INSERT INTO certificates (template_id, recipient_user_id, class_id, data, code, issued_by_user_id, verify_url) VALUES ($1,$2,$3,'{}',$4,$5,$6)`, r.TemplateID, userID, classID, code, "system", verifyURL)
		}
	}
	app := fiber.New()
	// Security & platform middleware
	// CORS
	app.Use(cors.New(cors.Config{
		AllowOrigins:     opts.AllowedOrigins,
		AllowCredentials: true,
		// Explicit headers to satisfy credentialed preflights
		AllowHeaders: "Content-Type, Authorization, X-Requested-With, Accept, Origin",
		AllowMethods: "GET,POST,PATCH,DELETE,OPTIONS",
	}))
	// Security headers
	app.Use(helmet.New())
	// HSTS advisory
	app.Use(func(c *fiber.Ctx) error {
		c.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		return c.Next()
	})
	// Optional CSRF protection (enable via CSRF_ENABLED=1)
	if strings.TrimSpace(os.Getenv("CSRF_ENABLED")) == "1" {
		app.Use(csrf.New(csrf.Config{
			CookieName:     "XSRF-TOKEN",
			CookieSecure:   true,
			CookieHTTPOnly: false,
			KeyLookup:      "header:X-CSRF-Token",
		}))
	}
	// Basic rate limit (per IP)
	app.Use(limiter.New(limiter.Config{
		Max:          120,
		Expiration:   1 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string { return "ip:" + c.IP() },
	}))

	// Optional Redis cache for read-heavy GETs
	var rdb *redis.Client
	if addr := strings.TrimSpace(os.Getenv("REDIS_ADDR")); addr != "" {
		db := 0
		if v := strings.TrimSpace(os.Getenv("REDIS_DB")); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				db = n
			}
		}
		rdb = redis.NewClient(&redis.Options{Addr: addr, Password: strings.TrimSpace(os.Getenv("REDIS_PASSWORD")), DB: db})
		app.Use(func(c *fiber.Ctx) error {
			if rdb == nil || c.Method() != http.MethodGet {
				return c.Next()
			}
			p := c.Path()
			// allowlist public/read-heavy paths
			allowed := p == "/v1/schools" || p == "/v1/classes" || strings.HasPrefix(p, "/v1/classes/") || strings.HasPrefix(p, "/v1/certificates/verify") || strings.HasPrefix(p, "/v1/announcements") || strings.HasPrefix(p, "/v1/years") || strings.HasPrefix(p, "/v1/terms") || strings.HasPrefix(p, "/v1/events")
			if !allowed {
				return c.Next()
			}
			cookie := c.Get("Cookie")
			key := "cache:" + p + "?" + c.Context().QueryArgs().String() + ":ck=" + cookie
			ctx := c.Context()
			if b, err := rdb.Get(ctx, key).Bytes(); err == nil && len(b) > 0 {
				c.Set("Content-Type", "application/json")
				return c.Send(b)
			}
			if err := c.Next(); err != nil {
				return err
			}
			if c.Response().StatusCode() == 200 && strings.Contains(strings.ToLower(string(c.Response().Header.ContentType())), "application/json") {
				_ = rdb.Set(ctx, key, append([]byte{}, c.Response().Body()...), 30*time.Second).Err()
			}
			return nil
		})
		// cache-busting after writes to relevant resources
		app.Use(func(c *fiber.Ctx) error {
			if rdb == nil || c.Method() == http.MethodGet || !strings.HasPrefix(c.Path(), "/v1/") {
				return c.Next()
			}
			// proceed with request first
			if err := c.Next(); err != nil {
				return err
			}
			if c.Response().StatusCode() >= 200 && c.Response().StatusCode() < 300 {
				ctx := c.Context()
				prefixes := []string{"cache:/v1/classes", "cache:/v1/schools", "cache:/v1/announcements", "cache:/v1/years", "cache:/v1/terms", "cache:/v1/events", "cache:/v1/certificates/verify"}
				for _, pref := range prefixes {
					var cursor uint64
					for {
						keys, cur, _ := rdb.Scan(ctx, cursor, pref+"*", 100).Result()
						cursor = cur
						if len(keys) > 0 {
							_ = rdb.Del(ctx, keys...).Err()
						}
						if cursor == 0 {
							break
						}
					}
				}
			}
			return nil
		})
	}

	// Optional 2FA requirement for sensitive routes
	if strings.TrimSpace(os.Getenv("REQUIRE_2FA_FOR_TUTORS")) == "1" {
		app.Use(func(c *fiber.Ctx) error {
			if c.Method() == http.MethodGet {
				return c.Next()
			}
			p := c.Path()
			// Write operations on classes/tests/certificates require 2FA
			if !(strings.HasPrefix(p, "/v1/classes") || strings.HasPrefix(p, "/v1/tests") || strings.HasPrefix(p, "/v1/certificates")) {
				return c.Next()
			}
			// Verify and check 2FA flags from Core
			req, _ := http.NewRequest("GET", strings.TrimRight(opts.CoreAPIBase, "/")+"/v1/auth/verify", nil)
			if v := c.Get("Authorization"); v != "" {
				req.Header.Set("Authorization", v)
			}
			if v := c.Get("Cookie"); v != "" {
				req.Header.Set("Cookie", v)
			}
			cli := &http.Client{Timeout: 3 * time.Second}
			resp, err := cli.Do(req)
			if err != nil {
				return fiber.ErrUnauthorized
			}
			defer resp.Body.Close()
			var raw map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&raw)
			data, _ := raw["data"].(map[string]any)
			ok := false
			if data != nil {
				// accept several possible flags from Core
				for _, k := range []string{"twofa", "2fa", "mfa", "mfaVerified"} {
					if v, okv := data[k]; okv {
						switch t := v.(type) {
						case bool:
							ok = t
						}
					}
				}
			}
			if !ok {
				return c.Status(403).JSON(fiber.Map{"success": false, "message": "2FA required for this action."})
			}
			return c.Next()
		})
	}

	// Session timeout (idle) — last-seen cookie; default 12h
	app.Use(func(c *fiber.Ctx) error {
		// Only guard API routes; allow OPTIONS
		if c.Method() == http.MethodOptions || !strings.HasPrefix(c.Path(), "/v1/") {
			return c.Next()
		}
		ttlMin := 720
		if v := strings.TrimSpace(os.Getenv("SESSION_IDLE_TIMEOUT_MINUTES")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				ttlMin = n
			}
		}
		now := time.Now().Unix()
		if v := c.Cookies("schools_last_seen"); v != "" {
			if sec, err := strconv.ParseInt(v, 10, 64); err == nil {
				if now-sec > int64(ttlMin*60) {
					return c.Status(401).JSON(fiber.Map{"success": false, "message": "Session expired due to inactivity."})
				}
			}
		}
		c.Cookie(&fiber.Cookie{Name: "schools_last_seen", Value: fmt.Sprintf("%d", now), HTTPOnly: true, Secure: true, SameSite: "Lax"})
		return c.Next()
	})

	// Email sender (noop if SMTP not configured)
	sendEmail := func(to, subject, body string) error {
		if strings.TrimSpace(opts.SMTPHost) == "" {
			// dev mode: log only
			fmt.Printf("[email-mock] to=%s subject=%s\n", to, subject)
			return nil
		}
		addr := fmt.Sprintf("%s:%s", strings.TrimSpace(opts.SMTPHost), strings.TrimSpace(opts.SMTPPort))
		// Basic RFC822-like message
		msg := []byte("To: " + to + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
			body)
		var auth smtp.Auth
		if strings.TrimSpace(opts.SMTPUsername) != "" {
			auth = smtp.PlainAuth("", strings.TrimSpace(opts.SMTPUsername), strings.TrimSpace(opts.SMTPPassword), strings.TrimSpace(opts.SMTPHost))
		}
		return smtp.SendMail(addr, auth, strings.TrimSpace(opts.SMTPFrom), []string{to}, msg)
	}

	// SMS sender (noop if provider not configured)
	sendSMS := func(to, body string) error {
		prov := strings.ToLower(strings.TrimSpace(opts.SMSProvider))
		if prov == "" {
			fmt.Printf("[sms-mock] to=%s body=%s\n", to, body)
			return nil
		}
		switch prov {
		case "twilio":
			sid := strings.TrimSpace(opts.TwilioAccountSID)
			tok := strings.TrimSpace(opts.TwilioAuthToken)
			from := strings.TrimSpace(opts.TwilioFrom)
			if sid == "" || tok == "" || from == "" {
				return fmt.Errorf("twilio env missing")
			}
			form := url.Values{}
			form.Set("To", to)
			form.Set("From", from)
			form.Set("Body", body)
			req, _ := http.NewRequest("POST", fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json", sid), strings.NewReader(form.Encode()))
			req.SetBasicAuth(sid, tok)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			cli := &http.Client{Timeout: 10 * time.Second}
			resp, err := cli.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 300 {
				return fmt.Errorf("twilio status %d", resp.StatusCode)
			}
			return nil
		default:
			return fmt.Errorf("sms provider %s not supported", prov)
		}
	}
	ao := strings.TrimSpace(opts.AllowedOrigins)
	allowCreds := true
	if ao == "" || ao == "*" || strings.Contains(ao, "*") {
		allowCreds = false
	}
	app.Use(cors.New(cors.Config{
		AllowOrigins:     ao,
		AllowMethods:     "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowHeaders:     "Authorization,Content-Type,Accept",
		AllowCredentials: allowCreds,
	}))
	// Optional certificate signer (ed25519) from env seed (base64)
	var certSigner ed25519.PrivateKey
	var certSignerAlg string
	if pkb64 := strings.TrimSpace(os.Getenv("CERT_PRIVATE_KEY")); pkb64 != "" {
		if raw, err := base64.StdEncoding.DecodeString(pkb64); err == nil && len(raw) == ed25519.SeedSize {
			certSigner = ed25519.NewKeyFromSeed(raw)
			certSignerAlg = "ed25519-sha256"
		}
	}

	app.Get("/v1/health", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"success": true}) })

	// Local uploads directory and static serving
	uploadDir := strings.TrimSpace(os.Getenv("UPLOAD_DIR"))
	if uploadDir == "" {
		uploadDir = "/tmp/uploads"
	}
	_ = os.MkdirAll(uploadDir, 0o755)
	app.Static("/uploads", uploadDir)
	// Audit logger helper
	logAudit := func(actor, action, targetType, targetID string, details any) {
		if opts.DB == nil {
			return
		}
		var d []byte
		if details != nil {
			d, _ = json.Marshal(details)
		}
		_, _ = opts.DB.Exec(`INSERT INTO audit_logs (actor_user_id, action, target_type, target_id, details) VALUES ($1,$2,$3,$4,COALESCE($5,'{}'::jsonb))`, actor, action, nullIfEmptyStr(targetType), nullIfEmptyStr(targetID), nullIfEmptyJSON(d))
	}
	// Upload feature flags
	uploadEnabled := true
	if v := strings.TrimSpace(os.Getenv("UPLOAD_ENABLED")); v != "" {
		uploadEnabled = strings.EqualFold(v, "true") || v == "1"
	}
	proxyUploadURL := strings.TrimSpace(os.Getenv("FILE_UPLOAD_PROXY_URL"))
	maxBytes := int64(2 * 1024 * 1024) // 2MB default for business docs
	if v := strings.TrimSpace(os.Getenv("UPLOAD_MAX_BYTES")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			maxBytes = n
		}
	}
	// Simple upload endpoint (local or proxy)
	app.Post("/v1/uploads", func(c *fiber.Ctx) error {
		if !uploadEnabled && proxyUploadURL == "" {
			return c.Status(403).JSON(fiber.Map{"success": false, "message": "Uploads are disabled"})
		}
		fh, err := c.FormFile("file")
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "Missing file"})
		}
		// Basic type guard
		ct := strings.ToLower(strings.TrimSpace(fh.Header.Get("Content-Type")))
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		allowedExts := map[string]string{
			".pdf":  "application/pdf",
			".png":  "image/",
			".jpg":  "image/",
			".jpeg": "image/",
			".webp": "image/",
			".heic": "image/",
			".heif": "image/",
			".mp4":  "video/",
			".mov":  "video/",
			".m4v":  "video/",
			".webm": "video/",
		}
		allowedDisplay := "PDF, PNG, JPG, JPEG, WEBP, HEIC/HEIF, MP4, MOV, M4V, WebM"
		pattern, ok := allowedExts[ext]
		if !ok {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "Unsupported file type. Allowed: " + allowedDisplay})
		}
		if ct != "" && ct != "application/octet-stream" && !strings.HasPrefix(ct, pattern) {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "File content type not allowed. Allowed: " + allowedDisplay})
		}
		// Size limit
		if fh.Size > 0 && fh.Size > maxBytes {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": fmt.Sprintf("File too large (max %d bytes)", maxBytes)})
		}
		// Proxy mode
		if proxyUploadURL != "" {
			file, err := fh.Open()
			if err != nil {
				return fiber.ErrBadRequest
			}
			defer file.Close()
			// Build multipart request
			var b bytes.Buffer
			w := multipart.NewWriter(&b)
			part, err := w.CreateFormFile("file", fh.Filename)
			if err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			if _, err := io.Copy(part, file); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			w.Close()
			req, _ := http.NewRequest("POST", proxyUploadURL, &b)
			req.Header.Set("Content-Type", w.FormDataContentType())
			if v := c.Get("Authorization"); v != "" {
				req.Header.Set("Authorization", v)
			}
			client := &http.Client{Timeout: 15 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				return c.Status(502).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			defer resp.Body.Close()
			var out map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&out)
			return c.Status(resp.StatusCode).JSON(out)
		}
		// Local save
		day := time.Now().Format("20060102")
		subdir := filepath.Join(uploadDir, day)
		if err := os.MkdirAll(subdir, 0o755); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		name := uuid.New().String() + ext
		dest := filepath.Join(subdir, name)
		if err := c.SaveFile(fh, dest); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		urlPath := "/uploads/" + day + "/" + name
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"url": urlPath, "filename": fh.Filename}})
	})

	// Auth verify against Core API
	getUserID := func(c *fiber.Ctx) (string, error) {
		authz := strings.TrimSpace(c.Get("Authorization"))
		if token := bearerToken(authz); token != "" && authVerifier != nil {
			if claims, err := authVerifier.Verify(token); err == nil {
				if uuid := strings.TrimSpace(claims.UUID); uuid != "" {
					return uuid, nil
				}
			} else {
				if errors.Is(err, coreauth.ErrTokenInvalid) || errors.Is(err, coreauth.ErrTokenExpired) || errors.Is(err, coreauth.ErrTokenMissing) {
					return "", fiber.ErrUnauthorized
				}
				if !errors.Is(err, coreauth.ErrJWKSUnavailable) {
					fmt.Printf("warn: coreauth verify failed: %v\n", err)
				}
			}
		}
		if coreAPIBase == "" {
			return "", fiber.ErrUnauthorized
		}
		req, _ := http.NewRequest(http.MethodPost, strings.TrimRight(coreAPIBase, "/")+"/v1/auth/verify", nil)
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		if v := c.Get("Cookie"); v != "" {
			req.Header.Set("Cookie", v)
		}
		if v := c.Get("Origin"); v != "" {
			req.Header.Set("Origin", v)
		}
		req.Header.Set("Accept", "application/json")
		resp, err := httpClientAuth.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		var raw map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return "", err
		}
		data, _ := raw["data"].(map[string]any)
		if data == nil {
			return "", fiber.ErrUnauthorized
		}
		if valid, _ := data["valid"].(bool); !valid {
			return "", fiber.ErrUnauthorized
		}
		if uuidStr, ok := data["uuid"].(string); ok && strings.TrimSpace(uuidStr) != "" {
			return strings.TrimSpace(uuidStr), nil
		}
		if uidAny, ok := data["uid"]; ok {
			switch v := uidAny.(type) {
			case float64:
				return fmt.Sprintf("%0.0f", v), nil
			case string:
				if s := strings.TrimSpace(v); s != "" {
					return s, nil
				}
			}
		}
		if uidStr, ok := data["userId"].(string); ok && strings.TrimSpace(uidStr) != "" {
			return strings.TrimSpace(uidStr), nil
		}
		return "", fiber.ErrUnauthorized
	}

	// --- AI settings endpoints ---
	app.Get("/v1/ai/settings", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var row struct {
			Enabled  *bool   `db:"ai_enabled"`
			Level    *string `db:"content_level"`
			Guardian *bool   `db:"guardian_controlled"`
		}
		_ = opts.DB.Get(&row, `SELECT ai_enabled, content_level, guardian_controlled FROM ai_settings WHERE user_id=$1`, uid)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"enabled": row.Enabled == nil || *row.Enabled, "contentLevel": coalesceString(row.Level), "guardianControlled": row.Guardian != nil && *row.Guardian}})
	})
	app.Patch("/v1/ai/settings", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var b struct {
			Enabled      *bool   `json:"enabled"`
			ContentLevel *string `json:"contentLevel"`
		}
		_ = c.BodyParser(&b)
		_, err = opts.DB.Exec(`INSERT INTO ai_settings (user_id, ai_enabled, content_level) VALUES ($1, COALESCE($2,TRUE), COALESCE($3,'general'))
			ON CONFLICT (user_id) DO UPDATE SET ai_enabled=COALESCE($2, ai_settings.ai_enabled), content_level=COALESCE($3, ai_settings.content_level), updated_at=now()`, uid, b.Enabled, b.ContentLevel)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Post("/v1/ai/settings/:child", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		child := c.Params("child")
		var ok bool
		_ = opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM guardians_children WHERE guardian_user_id=$1 AND child_user_id=$2)`, uid, child)
		if !ok {
			return fiber.ErrForbidden
		}
		var b struct {
			Enabled      *bool   `json:"enabled"`
			ContentLevel *string `json:"contentLevel"`
		}
		_ = c.BodyParser(&b)
		_, err = opts.DB.Exec(`INSERT INTO ai_settings (user_id, ai_enabled, content_level, guardian_controlled) VALUES ($1, COALESCE($2,TRUE), COALESCE($3,'general'), TRUE)
			ON CONFLICT (user_id) DO UPDATE SET ai_enabled=COALESCE($2, ai_settings.ai_enabled), content_level=COALESCE($3, ai_settings.content_level), guardian_controlled=TRUE, updated_at=now()`, child, b.Enabled, b.ContentLevel)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// User affiliation summary (independent vs schools and primary)
	app.Get("/v1/users/:id/affiliation", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		target := c.Params("id")
		if target == "me" || strings.TrimSpace(target) == "" {
			target = uid
		}
		if target != uid {
			var ok bool
			_ = opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM guardians_children WHERE guardian_user_id=$1 AND child_user_id=$2)`, uid, target)
			if !ok {
				return fiber.ErrForbidden
			}
		}
		type schoolAff struct {
			ID      string  `json:"id" db:"id"`
			Name    *string `json:"name" db:"name"`
			Primary bool    `json:"primary" db:"is_primary"`
		}
		rows := []schoolAff{}
		_ = opts.DB.Select(&rows, `SELECT s.id, s.info->>'name' AS name, sm.is_primary
			FROM school_members sm JOIN schools s ON s.id=sm.school_id
			WHERE sm.user_id=$1 AND sm.status='active' ORDER BY sm.is_primary DESC, s.created_at DESC`, target)
		aff := fiber.Map{"userId": target, "independent": len(rows) == 0, "schools": rows}
		return c.JSON(fiber.Map{"success": true, "data": aff})
	})
	// Platform admin checker
	isPlatformAdmin := func(uid string) bool {
		if opts.DB == nil || uid == "" {
			return false
		}
		var ok bool
		_ = opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM platform_admins WHERE user_id=$1)`, uid)
		return ok
	}
	// App role checker for current user via Core API
	hasSchoolsRole := func(c *fiber.Ctx, role string) bool {
		if strings.TrimSpace(opts.CoreAPIBase) == "" {
			return false
		}
		req, _ := http.NewRequest("GET", strings.TrimRight(opts.CoreAPIBase, "/")+"/v1/apps/schools/roles", nil)
		if ck := c.Get("Cookie"); ck != "" {
			req.Header.Set("Cookie", ck)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp == nil {
			return false
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return false
		}
		var out struct {
			Success bool     `json:"success"`
			Data    []string `json:"data"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		key := "schools." + strings.TrimSpace(role)
		for _, r := range out.Data {
			if r == key || r == role {
				return true
			}
		}
		return false
	}
	// --- Schools ---
	type school struct {
		ID          string    `json:"id" db:"id"`
		OwnerUserID string    `json:"ownerUserId" db:"owner_user_id"`
		Name        string    `json:"name" db:"name"`
		Description *string   `json:"description,omitempty" db:"description"`
		IsVerified  bool      `json:"isVerified" db:"is_verified"`
		CreatedAt   time.Time `json:"createdAt" db:"created_at"`
		UpdatedAt   time.Time `json:"updatedAt" db:"updated_at"`
	}
	app.Get("/v1/schools", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		rows := []school{}
		if err := opts.DB.Select(&rows, `SELECT id, owner_user_id, name, description, is_verified, created_at, updated_at FROM schools ORDER BY created_at DESC`); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	// List schools where current user is a member (optionally restricted by role)
	app.Get("/v1/schools/mine", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		role := strings.TrimSpace(strings.ToLower(c.Query("role")))
		rows := []school{}
		if role == "" || role == "any" {
			if err := opts.DB.Select(&rows, `SELECT s.id, s.owner_user_id, s.name, s.description, s.is_verified, s.created_at, s.updated_at
                FROM schools s JOIN school_members m ON m.school_id=s.id WHERE m.user_id=$1 AND m.status='active'
                ORDER BY s.created_at DESC`, uid); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		} else if role == "owner" {
			if err := opts.DB.Select(&rows, `SELECT id, owner_user_id, name, description, is_verified, created_at, updated_at FROM schools WHERE owner_user_id=$1 ORDER BY created_at DESC`, uid); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		} else {
			if err := opts.DB.Select(&rows, `SELECT s.id, s.owner_user_id, s.name, s.description, s.is_verified, s.created_at, s.updated_at
                FROM schools s JOIN school_members m ON m.school_id=s.id WHERE m.user_id=$1 AND m.role=$2 AND m.status='active'
                ORDER BY s.created_at DESC`, uid, role); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	type schoolIn struct {
		Name        string  `json:"name"`
		Description *string `json:"description"`
	}
	app.Post("/v1/schools", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var in schoolIn
		if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.Name) == "" {
			return fiber.ErrBadRequest
		}
		var out school
		err = opts.DB.Get(&out, `INSERT INTO schools (owner_user_id, name, description) VALUES ($1,$2,$3)
            RETURNING id, owner_user_id, name, description, is_verified, created_at, updated_at`, uid, in.Name, in.Description)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		// owner becomes admin member
		_, _ = opts.DB.Exec(`INSERT INTO school_members (school_id, user_id, role) VALUES ($1,$2,'admin') ON CONFLICT DO NOTHING`, out.ID, uid)
		// Grant app role schools.owner in Core API for this user (best-effort)
		go func(cookie string) {
			defer func() { _ = recover() }()
			if strings.TrimSpace(opts.CoreAPIBase) == "" {
				return
			}
			payload := map[string]string{"role": "owner"}
			b, _ := json.Marshal(payload)
			req, _ := http.NewRequest("POST", strings.TrimRight(opts.CoreAPIBase, "/")+"/v1/apps/schools/roles", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			if cookie != "" {
				req.Header.Set("Cookie", cookie)
			}
			_, _ = http.DefaultClient.Do(req)
		}(c.Get("Cookie"))
		return c.JSON(fiber.Map{"success": true, "data": out})
	})
	// --- Classes ---
	type class struct {
		ID           string    `json:"id" db:"id"`
		SchoolID     *string   `json:"schoolId,omitempty" db:"school_id"`
		TutorUserID  string    `json:"tutorUserId" db:"tutor_user_id"`
		Title        string    `json:"title" db:"title"`
		Description  *string   `json:"description,omitempty" db:"description"`
		Visibility   string    `json:"visibility" db:"visibility"`
		IsPaid       bool      `json:"isPaid" db:"is_paid"`
		PriceCents   int       `json:"priceCents" db:"price_cents"`
		StudentCount int       `json:"studentCount" db:"student_count"`
		SubjectCount int       `json:"subjectCount" db:"subject_count"`
		LessonCount  int       `json:"lessonCount" db:"lesson_count"`
		TestCount    int       `json:"testCount" db:"test_count"`
		Status       string    `json:"status" db:"status"`
		Enrolled     bool      `json:"isEnrolled" db:"is_enrolled"`
		CreatedAt    time.Time `json:"createdAt" db:"created_at"`
		UpdatedAt    time.Time `json:"updatedAt" db:"updated_at"`
	}
	app.Get("/v1/classes", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		schoolID := c.Query("school_id")
		mine := strings.EqualFold(c.Query("mine"), "1") || strings.EqualFold(c.Query("mine"), "true")
		tutorFilter := c.Query("tutor_user_id")
		uid, _ := getUserID(c) // optional; if present include owned/private
		if mine && uid == "" {
			return fiber.ErrUnauthorized
		}
		rows := []class{}
		if schoolID != "" {
			if err := opts.DB.Select(&rows, `SELECT c.id, c.school_id, c.tutor_user_id, c.title, c.description, c.visibility, c.is_paid, c.price_cents,
                    COALESCE(stats.student_count,0) AS student_count, COALESCE(subj.subject_count,0) AS subject_count,
                    COALESCE(less.lesson_count,0) AS lesson_count, COALESCE(tests.test_count,0) AS test_count,
                    c.status,
                    CASE WHEN enr.student_user_id IS NOT NULL THEN TRUE ELSE FALSE END AS is_enrolled,
                    c.created_at, c.updated_at
                    FROM classes c
                    LEFT JOIN (
                        SELECT class_id, COUNT(*) FILTER (WHERE status='active')::int AS student_count
                        FROM class_enrollments
                        GROUP BY class_id
                    ) stats ON stats.class_id=c.id
                    LEFT JOIN (
                        SELECT class_id, COUNT(*)::int AS subject_count FROM subjects WHERE status='active' GROUP BY class_id
                    ) subj ON subj.class_id=c.id
                    LEFT JOIN (
                        SELECT sub.class_id, COUNT(*)::int AS lesson_count
                        FROM lessons l
                        JOIN subjects sub ON sub.id=l.subject_id
                        WHERE l.status='active' AND sub.status='active'
                        GROUP BY sub.class_id
                    ) less ON less.class_id=c.id
                    LEFT JOIN (
                        SELECT class_id, COUNT(*)::int AS test_count FROM (
                            SELECT COALESCE(sub.class_id, sub2.class_id) AS class_id
                            FROM tests t
                            LEFT JOIN subjects sub ON sub.id=t.subject_id AND sub.status='active'
                            LEFT JOIN lessons l ON l.id=t.lesson_id AND l.status='active'
                            LEFT JOIN subjects sub2 ON sub2.id=l.subject_id AND sub2.status='active'
                            WHERE t.status='active'
                        ) q GROUP BY class_id
                    ) tests ON tests.class_id=c.id
                    LEFT JOIN class_enrollments enr ON enr.class_id=c.id AND enr.student_user_id=$2
                    WHERE (c.school_id=$1 OR c.tutor_user_id=$2 OR c.visibility='public') AND c.status='active'
                    ORDER BY c.created_at DESC`, schoolID, uid); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		} else if mine && uid != "" {
			if err := opts.DB.Select(&rows, `SELECT c.id, c.school_id, c.tutor_user_id, c.title, c.description, c.visibility, c.is_paid, c.price_cents,
                    COALESCE(stats.student_count,0) AS student_count, COALESCE(subj.subject_count,0) AS subject_count,
                    COALESCE(less.lesson_count,0) AS lesson_count, COALESCE(tests.test_count,0) AS test_count,
                    c.status,
                    CASE WHEN enr.student_user_id IS NOT NULL THEN TRUE ELSE FALSE END AS is_enrolled,
                    c.created_at, c.updated_at
                    FROM classes c
                    LEFT JOIN (
                        SELECT class_id, COUNT(*) FILTER (WHERE status='active')::int AS student_count
                        FROM class_enrollments
                        GROUP BY class_id
                    ) stats ON stats.class_id=c.id
                    LEFT JOIN (
                        SELECT class_id, COUNT(*)::int AS subject_count FROM subjects WHERE status='active' GROUP BY class_id
                    ) subj ON subj.class_id=c.id
                    LEFT JOIN (
                        SELECT sub.class_id, COUNT(*)::int AS lesson_count
                        FROM lessons l
                        JOIN subjects sub ON sub.id=l.subject_id
                        WHERE l.status='active' AND sub.status='active'
                        GROUP BY sub.class_id
                    ) less ON less.class_id=c.id
                    LEFT JOIN (
                        SELECT class_id, COUNT(*)::int AS test_count FROM (
                            SELECT COALESCE(sub.class_id, sub2.class_id) AS class_id
                            FROM tests t
                            LEFT JOIN subjects sub ON sub.id=t.subject_id AND sub.status='active'
                            LEFT JOIN lessons l ON l.id=t.lesson_id AND l.status='active'
                            LEFT JOIN subjects sub2 ON sub2.id=l.subject_id AND sub2.status='active'
                            WHERE t.status='active'
                        ) q GROUP BY class_id
                    ) tests ON tests.class_id=c.id
                    LEFT JOIN class_enrollments enr ON enr.class_id=c.id AND enr.student_user_id=$2
                    WHERE c.tutor_user_id=$1 AND c.status='active'
                    ORDER BY c.created_at DESC`, uid, uid); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		} else if tutorFilter != "" {
			if err := opts.DB.Select(&rows, `SELECT c.id, c.school_id, c.tutor_user_id, c.title, c.description, c.visibility, c.is_paid, c.price_cents,
                    COALESCE(stats.student_count,0) AS student_count, COALESCE(subj.subject_count,0) AS subject_count,
                    COALESCE(less.lesson_count,0) AS lesson_count, COALESCE(tests.test_count,0) AS test_count,
                    c.status,
                    CASE WHEN enr.student_user_id IS NOT NULL THEN TRUE ELSE FALSE END AS is_enrolled,
                    c.created_at, c.updated_at
                    FROM classes c
                    LEFT JOIN (
                        SELECT class_id, COUNT(*) FILTER (WHERE status='active')::int AS student_count
                        FROM class_enrollments
                        GROUP BY class_id
                    ) stats ON stats.class_id=c.id
                    LEFT JOIN (
                        SELECT class_id, COUNT(*)::int AS subject_count FROM subjects WHERE status='active' GROUP BY class_id
                    ) subj ON subj.class_id=c.id
                    LEFT JOIN (
                        SELECT sub.class_id, COUNT(*)::int AS lesson_count
                        FROM lessons l
                        JOIN subjects sub ON sub.id=l.subject_id
                        WHERE l.status='active' AND sub.status='active'
                        GROUP BY sub.class_id
                    ) less ON less.class_id=c.id
                    LEFT JOIN (
                        SELECT class_id, COUNT(*)::int AS test_count FROM (
                            SELECT COALESCE(sub.class_id, sub2.class_id) AS class_id
                            FROM tests t
                            LEFT JOIN subjects sub ON sub.id=t.subject_id AND sub.status='active'
                            LEFT JOIN lessons l ON l.id=t.lesson_id AND l.status='active'
                            LEFT JOIN subjects sub2 ON sub2.id=l.subject_id AND sub2.status='active'
                            WHERE t.status='active'
                        ) q GROUP BY class_id
                    ) tests ON tests.class_id=c.id
                    LEFT JOIN class_enrollments enr ON enr.class_id=c.id AND enr.student_user_id=$2
                    WHERE c.tutor_user_id=$1 AND c.status='active'
                    ORDER BY c.created_at DESC`, tutorFilter, uid); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		} else {
			if err := opts.DB.Select(&rows, `SELECT c.id, c.school_id, c.tutor_user_id, c.title, c.description, c.visibility, c.is_paid, c.price_cents,
                    COALESCE(stats.student_count,0) AS student_count, COALESCE(subj.subject_count,0) AS subject_count,
                    COALESCE(less.lesson_count,0) AS lesson_count, COALESCE(tests.test_count,0) AS test_count,
                    c.status,
                    CASE WHEN enr.student_user_id IS NOT NULL THEN TRUE ELSE FALSE END AS is_enrolled,
                    c.created_at, c.updated_at
                    FROM classes c
                    LEFT JOIN (
                        SELECT class_id, COUNT(*) FILTER (WHERE status='active')::int AS student_count
                        FROM class_enrollments
                        GROUP BY class_id
                    ) stats ON stats.class_id=c.id
                    LEFT JOIN (
                        SELECT class_id, COUNT(*)::int AS subject_count FROM subjects WHERE status='active' GROUP BY class_id
                    ) subj ON subj.class_id=c.id
                    LEFT JOIN (
                        SELECT sub.class_id, COUNT(*)::int AS lesson_count
                        FROM lessons l
                        JOIN subjects sub ON sub.id=l.subject_id
                        WHERE l.status='active' AND sub.status='active'
                        GROUP BY sub.class_id
                    ) less ON less.class_id=c.id
                    LEFT JOIN (
                        SELECT class_id, COUNT(*)::int AS test_count FROM (
                            SELECT COALESCE(sub.class_id, sub2.class_id) AS class_id
                            FROM tests t
                            LEFT JOIN subjects sub ON sub.id=t.subject_id AND sub.status='active'
                            LEFT JOIN lessons l ON l.id=t.lesson_id AND l.status='active'
                            LEFT JOIN subjects sub2 ON sub2.id=l.subject_id AND sub2.status='active'
                            WHERE t.status='active'
                        ) q GROUP BY class_id
                    ) tests ON tests.class_id=c.id
                    LEFT JOIN class_enrollments enr ON enr.class_id=c.id AND enr.student_user_id=$2
                    WHERE (c.visibility='public' OR c.tutor_user_id=$1) AND c.status='active'
                    ORDER BY c.created_at DESC`, uid, uid); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	type classIn struct {
		SchoolID    *string `json:"schoolId"`
		Title       string  `json:"title"`
		Description *string `json:"description"`
		Visibility  string  `json:"visibility"`
		IsPaid      *bool   `json:"isPaid"`
		PriceCents  *int    `json:"priceCents"`
	}
	app.Post("/v1/classes", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var in classIn
		if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.Title) == "" {
			return fiber.ErrBadRequest
		}
		vis := in.Visibility
		if vis == "" {
			vis = "school"
		}
		isPaid := false
		if in.IsPaid != nil {
			isPaid = *in.IsPaid
		}
		price := 0
		if in.PriceCents != nil {
			price = *in.PriceCents
		}
		if in.SchoolID != nil && *in.SchoolID != "" {
			okA, _ := auth.IsSchoolAdmin(opts.DB, *in.SchoolID, uid)
			okT, _ := auth.IsSchoolTutor(opts.DB, *in.SchoolID, uid)
			if !okA && !okT {
				return fiber.ErrForbidden
			}
		} else {
			// Independent class: require approved private tutor
			okPT, _ := auth.IsApprovedPrivateTutor(opts.DB, uid)
			if !okPT {
				return fiber.ErrForbidden
			}
		}
		var out class
		err = opts.DB.Get(&out, `INSERT INTO classes (school_id, tutor_user_id, title, description, visibility, is_paid, price_cents)
            VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, school_id, tutor_user_id, title, description, visibility, is_paid, price_cents, status, created_at, updated_at`,
			in.SchoolID, uid, in.Title, in.Description, vis, isPaid, price)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})
	app.Post("/v1/classes/:id/enroll", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		// Enforce prerequisites: recipient must already hold certs for all required classes
		var missing int
		_ = opts.DB.Get(&missing, `SELECT COUNT(*) FROM class_prerequisites p
             WHERE p.class_id=$1 AND NOT EXISTS (
               SELECT 1 FROM certificates ce WHERE ce.recipient_user_id=$2 AND ce.status='issued' AND ce.class_id=p.required_class_id
             )`, id, uid)
		if missing > 0 {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "Prerequisites not met for this class"})
		}
		if _, err := opts.DB.Exec(`INSERT INTO class_enrollments (class_id, student_user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		logAudit(uid, "class_enroll", "class", id, fiber.Map{"student": uid})
		return c.JSON(fiber.Map{"success": true})
	})
	app.Patch("/v1/classes/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		classID := c.Params("id")
		var existing struct {
			TutorUserID string  `db:"tutor_user_id"`
			SchoolID    *string `db:"school_id"`
		}
		if err := opts.DB.Get(&existing, `SELECT tutor_user_id, school_id FROM classes WHERE id=$1`, classID); err != nil {
			return fiber.ErrNotFound
		}
		if existing.TutorUserID != uid {
			allowed := false
			if existing.SchoolID != nil && *existing.SchoolID != "" {
				allowed, _ = auth.IsSchoolAdmin(opts.DB, *existing.SchoolID, uid)
			}
			if !allowed {
				return fiber.ErrForbidden
			}
		}
		var body struct {
			Title       *string `json:"title"`
			Description *string `json:"description"`
			Visibility  *string `json:"visibility"`
			IsPaid      *bool   `json:"isPaid"`
			PriceCents  *int    `json:"priceCents"`
			Status      *string `json:"status"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		setClauses := []string{}
		args := []any{}
		if body.Title != nil {
			setClauses = append(setClauses, "title=$"+strconv.Itoa(len(args)+1))
			args = append(args, strings.TrimSpace(*body.Title))
		}
		if body.Description != nil {
			setClauses = append(setClauses, "description=$"+strconv.Itoa(len(args)+1))
			desc := strings.TrimSpace(*body.Description)
			if desc == "" {
				args = append(args, nil)
			} else {
				args = append(args, desc)
			}
		}
		if body.Visibility != nil {
			vis := strings.ToLower(strings.TrimSpace(*body.Visibility))
			if vis != "public" && vis != "private" && vis != "school" {
				return fiber.ErrBadRequest
			}
			setClauses = append(setClauses, "visibility=$"+strconv.Itoa(len(args)+1))
			args = append(args, vis)
		}
		if body.IsPaid != nil {
			setClauses = append(setClauses, "is_paid=$"+strconv.Itoa(len(args)+1))
			args = append(args, *body.IsPaid)
		}
		if body.PriceCents != nil {
			price := *body.PriceCents
			if price < 0 {
				price = 0
			}
			setClauses = append(setClauses, "price_cents=$"+strconv.Itoa(len(args)+1))
			args = append(args, price)
		}
		if body.Status != nil {
			status := strings.ToLower(strings.TrimSpace(*body.Status))
			if status != "active" && status != "archived" && status != "deleted" {
				return fiber.ErrBadRequest
			}
			setClauses = append(setClauses, "status=$"+strconv.Itoa(len(args)+1))
			args = append(args, status)
		}
		if len(setClauses) == 0 {
			return c.JSON(fiber.Map{"success": true})
		}
		setClauses = append(setClauses, "updated_at=now()")
		args = append(args, classID)
		query := "UPDATE classes SET " + strings.Join(setClauses, ", ") + " WHERE id=$" + strconv.Itoa(len(args))
		if _, err := opts.DB.Exec(query, args...); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// --- Subjects ---
	type subject struct {
		ID          string  `json:"id" db:"id"`
		ClassID     string  `json:"classId" db:"class_id"`
		Title       string  `json:"title" db:"title"`
		Description *string `json:"description,omitempty" db:"description"`
		OrderIndex  int     `json:"orderIndex" db:"order_index"`
		Status      string  `json:"status" db:"status"`
	}
	app.Get("/v1/subjects", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		classID := c.Query("class_id")
		rows := []subject{}
		if classID == "" {
			return fiber.ErrBadRequest
		}
		if err := opts.DB.Select(&rows, `SELECT id, class_id, title, description, order_index, status FROM subjects WHERE class_id=$1 AND status='active' ORDER BY order_index ASC, title ASC`, classID); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	type subjectIn struct {
		ClassID     string  `json:"classId"`
		Title       string  `json:"title"`
		Description *string `json:"description"`
		OrderIndex  *int    `json:"orderIndex"`
	}
	app.Post("/v1/subjects", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		_ = uid
		var in subjectIn
		if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.ClassID) == "" {
			return fiber.ErrBadRequest
		}
		okCT, _ := auth.IsClassTutor(opts.DB, in.ClassID, uid)
		if !okCT {
			var sid *string
			_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, in.ClassID)
			if sid != nil && *sid != "" {
				okA, _ := auth.IsSchoolAdmin(opts.DB, *sid, uid)
				if !okA {
					return fiber.ErrForbidden
				}
			} else {
				return fiber.ErrForbidden
			}
		}
		ord := 0
		if in.OrderIndex != nil {
			ord = *in.OrderIndex
		}
		var out subject
		if err := opts.DB.Get(&out, `INSERT INTO subjects (class_id, title, description, order_index) VALUES ($1,$2,$3,$4) RETURNING id, class_id, title, description, order_index, status`, in.ClassID, in.Title, in.Description, ord); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})
	app.Patch("/v1/subjects/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		subjectID := c.Params("id")
		var meta struct {
			ClassID  string  `db:"class_id"`
			SchoolID *string `db:"school_id"`
		}
		if err := opts.DB.Get(&meta, `SELECT sub.class_id, cls.school_id
            FROM subjects sub
            JOIN classes cls ON cls.id=sub.class_id
            WHERE sub.id=$1`, subjectID); err != nil {
			return fiber.ErrNotFound
		}
		allowed, _ := auth.IsClassTutor(opts.DB, meta.ClassID, uid)
		if !allowed && meta.SchoolID != nil && *meta.SchoolID != "" {
			allowed, _ = auth.IsSchoolAdmin(opts.DB, *meta.SchoolID, uid)
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct {
			Status *string `json:"status"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		setClauses := []string{}
		args := []any{}
		if body.Status != nil {
			status := strings.ToLower(strings.TrimSpace(*body.Status))
			if status != "active" && status != "archived" && status != "deleted" {
				return fiber.ErrBadRequest
			}
			setClauses = append(setClauses, "status=$"+strconv.Itoa(len(args)+1))
			args = append(args, status)
		}
		if len(setClauses) == 0 {
			return c.JSON(fiber.Map{"success": true})
		}
		args = append(args, subjectID)
		query := "UPDATE subjects SET " + strings.Join(setClauses, ", ") + " WHERE id=$" + strconv.Itoa(len(args))
		if _, err := opts.DB.Exec(query, args...); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// --- Lessons ---
	type lesson struct {
		ID         string          `json:"id" db:"id"`
		SubjectID  string          `json:"subjectId" db:"subject_id"`
		Title      string          `json:"title" db:"title"`
		Type       string          `json:"type" db:"type"`
		Content    json.RawMessage `json:"content,omitempty" db:"content"`
		OrderIndex int             `json:"orderIndex" db:"order_index"`
		IsFree     bool            `json:"isFree" db:"is_free"`
		Status     string          `json:"status" db:"status"`
	}
	app.Get("/v1/lessons", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		sid := c.Query("subject_id")
		if sid == "" {
			return fiber.ErrBadRequest
		}
		rows := []lesson{}
		if err := opts.DB.Select(&rows, `SELECT id, subject_id, title, type, COALESCE(content,'null'::jsonb) AS content, order_index, is_free, status FROM lessons WHERE subject_id=$1 AND status='active' ORDER BY order_index ASC, title ASC`, sid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	type lessonIn struct {
		SubjectID  string          `json:"subjectId"`
		Title      string          `json:"title"`
		Type       string          `json:"type"`
		Content    json.RawMessage `json:"content"`
		OrderIndex *int            `json:"orderIndex"`
		IsFree     *bool           `json:"isFree"`
	}
	app.Post("/v1/lessons", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var in lessonIn
		if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.SubjectID) == "" || strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Type) == "" {
			return fiber.ErrBadRequest
		}
		var classID string
		if err := opts.DB.Get(&classID, `SELECT class_id FROM subjects WHERE id=$1`, in.SubjectID); err != nil {
			return fiber.ErrBadRequest
		}
		okCT, _ := auth.IsClassTutor(opts.DB, classID, uid)
		var okA bool
		if !okCT {
			var sid *string
			_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, classID)
			if sid != nil && *sid != "" {
				okA, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
			}
		}
		if !okCT && !okA {
			return fiber.ErrForbidden
		}
		ord := 0
		if in.OrderIndex != nil {
			ord = *in.OrderIndex
		}
		free := false
		if in.IsFree != nil {
			free = *in.IsFree
		}
		var out lesson
		if err := opts.DB.Get(&out, `INSERT INTO lessons (subject_id, title, type, content, order_index, is_free) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, subject_id, title, type, COALESCE(content,'null'::jsonb) AS content, order_index, is_free, status`, in.SubjectID, in.Title, in.Type, nullIfEmptyJSON(in.Content), ord, free); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})
	app.Patch("/v1/lessons/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		lessonID := c.Params("id")
		var meta struct {
			ClassID  string  `db:"class_id"`
			SchoolID *string `db:"school_id"`
		}
		if err := opts.DB.Get(&meta, `SELECT cls.id AS class_id, cls.school_id
            FROM lessons l
            JOIN subjects sub ON sub.id=l.subject_id
            JOIN classes cls ON cls.id=sub.class_id
            WHERE l.id=$1`, lessonID); err != nil {
			return fiber.ErrNotFound
		}
		allowed, _ := auth.IsClassTutor(opts.DB, meta.ClassID, uid)
		if !allowed && meta.SchoolID != nil && *meta.SchoolID != "" {
			allowed, _ = auth.IsSchoolAdmin(opts.DB, *meta.SchoolID, uid)
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct {
			Status *string `json:"status"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		setClauses := []string{}
		args := []any{}
		if body.Status != nil {
			status := strings.ToLower(strings.TrimSpace(*body.Status))
			if status != "active" && status != "archived" && status != "deleted" {
				return fiber.ErrBadRequest
			}
			setClauses = append(setClauses, "status=$"+strconv.Itoa(len(args)+1))
			args = append(args, status)
		}
		if len(setClauses) == 0 {
			return c.JSON(fiber.Map{"success": true})
		}
		args = append(args, lessonID)
		query := "UPDATE lessons SET " + strings.Join(setClauses, ", ") + " WHERE id=$" + strconv.Itoa(len(args))
		if _, err := opts.DB.Exec(query, args...); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// --- Tests ---
	type test struct {
		ID          string    `json:"id" db:"id"`
		SchoolID    *string   `json:"schoolId,omitempty" db:"school_id"`
		SubjectID   *string   `json:"subjectId,omitempty" db:"subject_id"`
		LessonID    *string   `json:"lessonId,omitempty" db:"lesson_id"`
		Title       string    `json:"title" db:"title"`
		Description *string   `json:"description,omitempty" db:"description"`
		Visibility  string    `json:"visibility" db:"visibility"`
		Status      string    `json:"status" db:"status"`
		CreatedBy   string    `json:"createdByUserId" db:"created_by_user_id"`
		CreatedAt   time.Time `json:"createdAt" db:"created_at"`
	}
	type testQuestion struct {
		ID         string          `json:"id" db:"id"`
		TestID     string          `json:"testId" db:"test_id"`
		QType      string          `json:"qtype" db:"qtype"`
		Prompt     string          `json:"prompt" db:"prompt"`
		Options    json.RawMessage `json:"options,omitempty" db:"options"`
		Answer     json.RawMessage `json:"answer,omitempty" db:"answer"`
		Points     int             `json:"points" db:"points"`
		OrderIndex int             `json:"orderIndex" db:"order_index"`
	}
	app.Get("/v1/tests", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		sid := c.Query("subject_id")
		lid := c.Query("lesson_id")
		rows := []test{}
		var err error
		if lid != "" {
			err = opts.DB.Select(&rows, `SELECT id, school_id, subject_id, lesson_id, title, description, visibility, status, created_by_user_id, created_at FROM tests WHERE lesson_id=$1 AND status='active' ORDER BY created_at DESC`, lid)
		} else if sid != "" {
			err = opts.DB.Select(&rows, `SELECT id, school_id, subject_id, lesson_id, title, description, visibility, status, created_by_user_id, created_at FROM tests WHERE subject_id=$1 AND status='active' ORDER BY created_at DESC`, sid)
		} else {
			err = opts.DB.Select(&rows, `SELECT id, school_id, subject_id, lesson_id, title, description, visibility, status, created_by_user_id, created_at FROM tests WHERE status='active' ORDER BY created_at DESC`)
		}
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	type testIn struct {
		SchoolID    *string `json:"schoolId"`
		SubjectID   *string `json:"subjectId"`
		LessonID    *string `json:"lessonId"`
		Title       string  `json:"title"`
		Description *string `json:"description"`
		Visibility  string  `json:"visibility"`
	}
	app.Post("/v1/tests", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var in testIn
		if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.Title) == "" {
			return fiber.ErrBadRequest
		}
		vis := in.Visibility
		if vis == "" {
			vis = "private"
		}
		var out test
		if err := opts.DB.Get(&out, `INSERT INTO tests (school_id, subject_id, lesson_id, title, description, visibility, created_by_user_id)
            VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, school_id, subject_id, lesson_id, title, description, visibility, status, created_by_user_id, created_at`, in.SchoolID, in.SubjectID, in.LessonID, in.Title, in.Description, vis, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})
	app.Patch("/v1/tests/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		testID := c.Params("id")
		var meta struct {
			Status   string  `db:"status"`
			ClassID  *string `db:"class_id"`
			SchoolID *string `db:"school_id"`
		}
		if err := opts.DB.Get(&meta, `SELECT t.status, COALESCE(c.id, c2.id) AS class_id, COALESCE(c.school_id, c2.school_id) AS school_id
			FROM tests t
			LEFT JOIN subjects sub ON sub.id=t.subject_id
			LEFT JOIN classes c ON c.id=sub.class_id
			LEFT JOIN lessons l ON l.id=t.lesson_id
			LEFT JOIN subjects sub2 ON sub2.id=l.subject_id
			LEFT JOIN classes c2 ON c2.id=sub2.class_id
			WHERE t.id=$1`, testID); err != nil {
			return fiber.ErrNotFound
		}
		allowed := false
		if meta.ClassID != nil && *meta.ClassID != "" {
			allowed, _ = auth.IsClassTutor(opts.DB, *meta.ClassID, uid)
			if !allowed && meta.SchoolID != nil && *meta.SchoolID != "" {
				allowed, _ = auth.IsSchoolAdmin(opts.DB, *meta.SchoolID, uid)
			}
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct {
			Status *string `json:"status"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		setClauses := []string{}
		args := []any{}
		if body.Status != nil {
			status := strings.ToLower(strings.TrimSpace(*body.Status))
			if status != "active" && status != "archived" && status != "deleted" {
				return fiber.ErrBadRequest
			}
			setClauses = append(setClauses, "status=$"+strconv.Itoa(len(args)+1))
			args = append(args, status)
		}
		if len(setClauses) == 0 {
			return c.JSON(fiber.Map{"success": true})
		}
		args = append(args, testID)
		query := "UPDATE tests SET " + strings.Join(setClauses, ", ") + " WHERE id=$" + strconv.Itoa(len(args))
		if _, err := opts.DB.Exec(query, args...); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// Get a specific test with questions (if permitted)
	app.Get("/v1/tests/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var t test
		if err := opts.DB.Get(&t, `SELECT id, school_id, subject_id, lesson_id, title, description, visibility, status, created_by_user_id, created_at FROM tests WHERE id=$1`, id); err != nil {
			return fiber.ErrNotFound
		}
		if t.Status == "deleted" {
			return fiber.ErrNotFound
		}
		// Permission check similar to search visibility for tests; also resolve classId and canEdit
		var link struct {
			ClassID  *string `db:"class_id"`
			SchoolID *string `db:"school_id"`
		}
		_ = opts.DB.Get(&link, `SELECT COALESCE(c.id, c2.id) AS class_id, COALESCE(c.school_id, c2.school_id) AS school_id FROM tests t
            LEFT JOIN subjects sub ON sub.id=t.subject_id
            LEFT JOIN classes c ON c.id=sub.class_id
            LEFT JOIN lessons l ON l.id=t.lesson_id
            LEFT JOIN subjects sub2 ON sub2.id=l.subject_id
            LEFT JOIN classes c2 ON c2.id=sub2.class_id
            WHERE t.id=$1 LIMIT 1`, id)
		classID := link.ClassID
		schoolID := link.SchoolID
		allow := t.Visibility == "public"
		canEdit := false
		if classID != nil && *classID != "" {
			var tutorID string
			_ = opts.DB.Get(&tutorID, `SELECT tutor_user_id FROM classes WHERE id=$1`, classID)
			if tutorID == uid {
				allow = true
				canEdit = true
			}
			if !allow {
				var exists bool
				_ = opts.DB.Get(&exists, `SELECT EXISTS (SELECT 1 FROM class_enrollments WHERE class_id=$1 AND student_user_id=$2)`, classID, uid)
				if exists {
					allow = true
				}
			}
			if schoolID != nil && *schoolID != "" {
				if !allow {
					allow, _ = auth.IsSchoolMember(opts.DB, *schoolID, uid)
				}
				if !canEdit {
					canEdit, _ = auth.IsSchoolAdmin(opts.DB, *schoolID, uid)
				}
			}
		}
		if canEdit {
			allow = true
		}
		if t.Status == "archived" && !canEdit {
			return fiber.ErrForbidden
		}
		if !allow {
			return fiber.ErrForbidden
		}
		// Load questions
		qs := []testQuestion{}
		if err := opts.DB.Select(&qs, `SELECT id, test_id, qtype, prompt, COALESCE(options,'null'::jsonb) AS options, COALESCE(answer,'null'::jsonb) AS answer, points, order_index FROM test_questions WHERE test_id=$1 ORDER BY order_index, id`, id); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"test": t, "questions": qs, "classId": classID, "canEdit": canEdit}})
	})
	// Create a question under a test (tutor/school admin only)
	type questionIn struct {
		QType      string          `json:"qtype"`
		Prompt     string          `json:"prompt"`
		Options    json.RawMessage `json:"options"`
		Answer     json.RawMessage `json:"answer"`
		Points     *int            `json:"points"`
		OrderIndex *int            `json:"orderIndex"`
	}
	app.Post("/v1/tests/:id/questions", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		// Resolve related class for permission checks
		var classID *string
		_ = opts.DB.Get(&classID, `SELECT c.id FROM tests t
            LEFT JOIN subjects sub ON sub.id=t.subject_id
            LEFT JOIN classes c ON c.id=sub.class_id
            LEFT JOIN lessons l ON l.id=t.lesson_id
            LEFT JOIN subjects sub2 ON sub2.id=l.subject_id
            LEFT JOIN classes c2 ON c2.id=sub2.class_id
            WHERE t.id=$1 LIMIT 1`, id)
		var allow bool
		if classID != nil && *classID != "" {
			allow, _ = auth.IsClassTutor(opts.DB, *classID, uid)
			if !allow {
				var sid *string
				_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, classID)
				if sid != nil && *sid != "" {
					allow, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
				}
			}
		}
		if !allow {
			return fiber.ErrForbidden
		}
		var in questionIn
		if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.QType) == "" || strings.TrimSpace(in.Prompt) == "" {
			return fiber.ErrBadRequest
		}
		// Normalize frontend constants to backend qtype
		qt := strings.ToLower(strings.TrimSpace(in.QType))
		switch qt {
		case "multiple_choice":
			qt = "mcq"
		case "multiple_select":
			qt = "mcq" // require options.allowMultiple=true for multiple
		case "true_false":
			qt = "truefalse"
		case "fill_blank":
			qt = "fillblank"
		case "short_answer":
			qt = "short"
		case "drag_drop":
			qt = "dragdrop"
		}
		pts := 1
		if in.Points != nil {
			pts = *in.Points
		}
		ord := 0
		if in.OrderIndex != nil {
			ord = *in.OrderIndex
		}
		var out testQuestion
		if err := opts.DB.Get(&out, `INSERT INTO test_questions (test_id, qtype, prompt, options, answer, points, order_index)
            VALUES ($1,$2,$3,$4,$5,$6,$7)
            RETURNING id, test_id, qtype, prompt, COALESCE(options,'null'::jsonb) AS options, COALESCE(answer,'null'::jsonb) AS answer, points, order_index`, id, qt, in.Prompt, nullIfEmptyJSON(in.Options), nullIfEmptyJSON(in.Answer), pts, ord); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})
	// --- Test Attempts ---
	type attempt struct {
		ID            string          `json:"id" db:"id"`
		TestID        string          `json:"testId" db:"test_id"`
		StudentUserID string          `json:"studentUserId" db:"student_user_id"`
		Score         *float64        `json:"score,omitempty" db:"score"`
		Status        string          `json:"status" db:"status"`
		SubmittedAt   *time.Time      `json:"submittedAt,omitempty" db:"submitted_at"`
		Responses     json.RawMessage `json:"responses,omitempty" db:"responses"`
		Grading       json.RawMessage `json:"grading,omitempty" db:"grading"`
	}
	app.Post("/v1/tests/:id/attempts/start", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		// Upsert-like: if existing in_progress, return it; else create
		var row attempt
		err = opts.DB.Get(&row, `SELECT id, test_id, student_user_id, score, status, submitted_at, COALESCE(responses,'null'::jsonb) AS responses, COALESCE(grading,'null'::jsonb) AS grading FROM test_attempts WHERE test_id=$1 AND student_user_id=$2`, id, uid)
		if err == nil {
			if row.Status == "in_progress" {
				return c.JSON(fiber.Map{"success": true, "data": row})
			}
			// existing submitted: create a new in_progress (one active at a time per test per user is typical; we'll replace)
		}
		if err := opts.DB.Get(&row, `INSERT INTO test_attempts (test_id, student_user_id, status) VALUES ($1,$2,'in_progress')
            RETURNING id, test_id, student_user_id, score, status, submitted_at, COALESCE(responses,'null'::jsonb) AS responses, COALESCE(grading,'null'::jsonb) AS grading`, id, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": row})
	})
	// List attempts for a test (tutor/school admin only)
	app.Get("/v1/tests/:id/attempts", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		tid := c.Params("id")
		// Resolve related class for permission checks
		var classID *string
		_ = opts.DB.Get(&classID, `SELECT c.id FROM tests t
			LEFT JOIN subjects sub ON sub.id=t.subject_id
			LEFT JOIN classes c ON c.id=sub.class_id
			LEFT JOIN lessons l ON l.id=t.lesson_id
			LEFT JOIN subjects sub2 ON sub2.id=l.subject_id
			LEFT JOIN classes c2 ON c2.id=sub2.class_id
			WHERE t.id=$1 LIMIT 1`, tid)
		allow := false
		if classID != nil && *classID != "" {
			allow, _ = auth.IsClassTutor(opts.DB, *classID, uid)
			if !allow {
				var sid *string
				_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, classID)
				if sid != nil && *sid != "" {
					allow, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
				}
			}
		}
		if !allow {
			return fiber.ErrForbidden
		}
		status := strings.TrimSpace(strings.ToLower(c.Query("status")))
		type row struct {
			ID        string          `json:"id" db:"id"`
			StudentID string          `json:"studentUserId" db:"student_user_id"`
			Score     *float64        `json:"score" db:"score"`
			Status    string          `json:"status" db:"status"`
			Submitted *time.Time      `json:"submittedAt" db:"submitted_at"`
			Responses json.RawMessage `json:"responses" db:"responses"`
			Grading   json.RawMessage `json:"grading" db:"grading"`
		}
		rows := []row{}
		if status == "" || status == "any" {
			if err := opts.DB.Select(&rows, `SELECT id, student_user_id, score, status, submitted_at, COALESCE(responses,'null'::jsonb) AS responses, COALESCE(grading,'null'::jsonb) AS grading FROM test_attempts WHERE test_id=$1 ORDER BY submitted_at DESC NULLS LAST, id DESC LIMIT 500`, tid); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		} else {
			if err := opts.DB.Select(&rows, `SELECT id, student_user_id, score, status, submitted_at, COALESCE(responses,'null'::jsonb) AS responses, COALESCE(grading,'null'::jsonb) AS grading FROM test_attempts WHERE test_id=$1 AND status=$2 ORDER BY submitted_at DESC NULLS LAST, id DESC LIMIT 500`, tid, status); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	// Manually grade an attempt (tutor/school admin only)
	app.Patch("/v1/tests/:id/attempts/:attemptId/grade", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		tid := c.Params("id")
		aid := c.Params("attemptId")
		// Permission same as list attempts
		var classID *string
		_ = opts.DB.Get(&classID, `SELECT c.id FROM tests t
			LEFT JOIN subjects sub ON sub.id=t.subject_id
			LEFT JOIN classes c ON c.id=sub.class_id
			LEFT JOIN lessons l ON l.id=t.lesson_id
			LEFT JOIN subjects sub2 ON sub2.id=l.subject_id
			LEFT JOIN classes c2 ON c2.id=sub2.class_id
			WHERE t.id=$1 LIMIT 1`, tid)
		allow := false
		if classID != nil && *classID != "" {
			allow, _ = auth.IsClassTutor(opts.DB, *classID, uid)
			if !allow {
				var sid *string
				_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, classID)
				if sid != nil && *sid != "" {
					allow, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
				}
			}
		}
		if !allow {
			return fiber.ErrForbidden
		}
		var body struct {
			Score   *float64        `json:"score"`
			Grading json.RawMessage `json:"grading"`
			Status  *string         `json:"status"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		// Default status to graded
		st := "graded"
		if body.Status != nil {
			s := strings.ToLower(strings.TrimSpace(*body.Status))
			if s != "graded" && s != "submitted" && s != "returned" {
				return fiber.ErrBadRequest
			}
			st = s
		}
		// Update
		if _, err := opts.DB.Exec(`UPDATE test_attempts SET grading=COALESCE($1, grading), score=$2, status=$3, submitted_at=COALESCE(submitted_at, now()) WHERE id=$4 AND test_id=$5`, nullIfEmptyJSON(body.Grading), body.Score, st, aid, tid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Patch("/v1/tests/:id/attempts/save", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var body struct {
			Responses json.RawMessage `json:"responses"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		if _, err := opts.DB.Exec(`UPDATE test_attempts SET responses=$1 WHERE test_id=$2 AND student_user_id=$3 AND status='in_progress'`, nullIfEmptyJSON(body.Responses), id, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Post("/v1/tests/:id/attempts/submit", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var body struct {
			Responses map[string]any `json:"responses"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		// Load questions for grading
		var qs []struct {
			ID, QType, Prompt  string
			Options, Answer    json.RawMessage
			Points, OrderIndex int
		}
		if err := opts.DB.Select(&qs, `SELECT id, qtype, prompt, COALESCE(options,'null'::jsonb) AS options, COALESCE(answer,'null'::jsonb) AS answer, points, order_index FROM test_questions WHERE test_id=$1 ORDER BY order_index, id`, id); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		earned := 0.0
		max := 0.0
		details := map[string]any{}
		// Helper decoders
		getBool := func(v any) (bool, bool) {
			b, ok := v.(bool)
			if ok {
				return b, true
			}
			if s, ok := v.(string); ok {
				return strings.ToLower(strings.TrimSpace(s)) == "true", true
			}
			return false, false
		}
		getStr := func(v any) (string, bool) {
			if s, ok := v.(string); ok {
				return s, true
			}
			return "", false
		}
		getStrSlice := func(v any) []string {
			if v == nil {
				return nil
			}
			if arr, ok := v.([]any); ok {
				out := []string{}
				for _, x := range arr {
					if s, ok := x.(string); ok {
						out = append(out, s)
					}
				}
				return out
			}
			return nil
		}
		getNum := func(v any) (float64, bool) {
			switch t := v.(type) {
			case float64:
				return t, true
			case int:
				return float64(t), true
			case string:
				if f, e := strconv.ParseFloat(strings.TrimSpace(t), 64); e == nil {
					return f, true
				}
			}
			return 0, false
		}
		for _, q := range qs {
			// Only auto-grade objective types
			qt := strings.ToLower(strings.TrimSpace(q.QType))
			objective := map[string]bool{"mcq": true, "truefalse": true, "match": true, "ordering": true, "fillblank": true, "numeric": true}
			if !objective[qt] {
				continue
			}
			max += float64(q.Points)
			resp := body.Responses[q.ID]
			var opts map[string]any
			_ = json.Unmarshal(q.Options, &opts)
			var ans map[string]any
			_ = json.Unmarshal(q.Answer, &ans)
			correct := false
			partial := 0.0
			isMulti := false
			switch qt {
			case "mcq":
				allowMultiple, _ := opts["allowMultiple"].(bool)
				isMulti = allowMultiple
				if allowMultiple {
					want := getStrSlice(ans["keys"])
					got := getStrSlice(resp)
					if len(want) > 0 {
						// partial via Jaccard index: |W∩G| / |W∪G|
						wset := map[string]bool{}
						for _, k := range want {
							wset[k] = true
						}
						gset := map[string]bool{}
						for _, k := range got {
							gset[k] = true
						}
						inter := 0.0
						uni := 0.0
						// union keys
						ukeys := map[string]bool{}
						for k := range wset {
							ukeys[k] = true
						}
						for k := range gset {
							ukeys[k] = true
						}
						for k := range ukeys {
							uni += 1
							if wset[k] && gset[k] {
								inter += 1
							}
						}
						if uni > 0 {
							partial = inter / uni
						}
					}
				} else {
					w, _ := getStr(ans["key"])
					g, _ := getStr(resp)
					correct = (w != "" && w == g)
				}
			case "truefalse":
				w, _ := getBool(ans["value"])
				g, _ := getBool(resp)
				correct = (w == g)
			case "match":
				// answer.pairs: [[left,right],...]
				pairsAny, _ := ans["pairs"].([]any)
				want := map[string]string{}
				for _, p := range pairsAny {
					if arr, ok := p.([]any); ok && len(arr) >= 2 {
						l, _ := getStr(arr[0])
						r, _ := getStr(arr[1])
						if l != "" {
							want[l] = r
						}
					}
				}
				// resp can be object { leftId: rightId }
				got := map[string]string{}
				if m, ok := resp.(map[string]any); ok {
					for lk, rv := range m {
						rs, _ := getStr(rv)
						got[lk] = rs
					}
				}
				// score per-left correct
				per := 0.0
				total := float64(len(want))
				if total > 0 {
					for l, r := range want {
						if got[l] == r {
							per += 1
						}
					}
					partial = per / total
				}
			case "ordering":
				// answer.order: [id1,id2,...]
				want := getStrSlice(ans["order"])
				got := getStrSlice(resp)
				if len(want) > 1 && len(got) == len(want) {
					// adjacency-based partial: match adjacent pairs
					wp := map[string]string{}
					for i := 0; i < len(want)-1; i++ {
						wp[want[i]] = want[i+1]
					}
					matches := 0.0
					for i := 0; i < len(got)-1; i++ {
						if wp[got[i]] == got[i+1] {
							matches += 1
						}
					}
					partial = matches / float64(len(want)-1)
					if partial == 1.0 {
						correct = true
					}
				} else if len(want) > 0 && len(got) == len(want) {
					// single item or empty adjacency; require exact
					ok := true
					for i := range want {
						if want[i] != got[i] {
							ok = false
							break
						}
					}
					correct = ok
				}
			case "fillblank":
				// answer: { blankId: value }
				want := map[string]string{}
				for k, v := range ans {
					if s, ok := v.(string); ok {
						want[k] = s
					}
				}
				got := map[string]string{}
				if m, ok := resp.(map[string]any); ok {
					for k, v := range m {
						if s, ok := v.(string); ok {
							got[k] = s
						}
					}
				}
				// case-insensitive by default; synonyms optional in options.blanks[*].synonyms
				blanks, _ := opts["blanks"].([]any)
				total := float64(len(want))
				per := 0.0
				if total > 0 {
					for _, bAny := range blanks {
						if b, ok := bAny.(map[string]any); ok {
							id, _ := getStr(b["id"])
							wantVal := strings.TrimSpace(strings.ToLower(want[id]))
							gotVal := strings.TrimSpace(strings.ToLower(got[id]))
							if wantVal == "" {
								continue
							}
							if gotVal == wantVal {
								per += 1
								continue
							}
							if syn, ok := b["synonyms"].([]any); ok {
								for _, s := range syn {
									if ss, ok := s.(string); ok && strings.ToLower(strings.TrimSpace(ss)) == gotVal {
										per += 1
										break
									}
								}
							}
						}
					}
					partial = per / total
				}
			case "numeric":
				want, _ := getNum(ans["value"])
				got, ok := getNum(resp)
				if ok {
					tol := 0.0
					if r, ok := opts["tolerance"].(float64); ok {
						tol = r
					}
					if rng, ok := opts["range"].(map[string]any); ok {
						min, _ := getNum(rng["min"])
						max, _ := getNum(rng["max"])
						correct = (got >= min && got <= max)
					} else {
						correct = (math.Abs(got-want) <= tol)
					}
				}
			}
			// accumulate
			if qt == "match" || qt == "fillblank" || (qt == "mcq" && isMulti) || qt == "ordering" {
				earned += float64(q.Points) * partial
			} else if correct {
				earned += float64(q.Points)
			}
			details[q.ID] = map[string]any{"correct": correct, "partial": partial}
		}
		var score *float64
		if max > 0 {
			s := (earned / max) * 100.0
			score = &s
		}
		gradingJSON, _ := json.Marshal(fiber.Map{"earned": earned, "max": max, "score": score})
		respsJSON, _ := json.Marshal(body.Responses)
		if _, err := opts.DB.Exec(`UPDATE test_attempts SET responses=$1, grading=$2, score=$3, status='submitted', submitted_at=now() WHERE test_id=$4 AND student_user_id=$5`, respsJSON, gradingJSON, score, id, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		// Trigger automation
		var link struct {
			ClassID *string `db:"class_id"`
		}
		_ = opts.DB.Get(&link, `SELECT COALESCE(c.id, c2.id) AS class_id FROM tests t
            LEFT JOIN subjects sub ON sub.id=t.subject_id
            LEFT JOIN classes c ON c.id=sub.class_id
            LEFT JOIN lessons l ON l.id=t.lesson_id
            LEFT JOIN subjects sub2 ON sub2.id=l.subject_id
            LEFT JOIN classes c2 ON c2.id=sub2.class_id
            WHERE t.id=$1 LIMIT 1`, id)
		if link.ClassID != nil && *link.ClassID != "" {
			evaluateCerts(*link.ClassID, uid)
		}
		evaluateCerts(*link.ClassID, uid)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"earned": earned, "max": max, "score": score}})
	})
	// --- Guardians ---
	app.Get("/v1/guardians/children", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var rows []struct {
			Child string `json:"childUserId" db:"child_user_id"`
		}
		if err := opts.DB.Select(&rows, `SELECT child_user_id FROM guardians_children WHERE guardian_user_id=$1`, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/guardians/link", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			ChildUserID string `json:"childUserId"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.ChildUserID) == "" {
			return fiber.ErrBadRequest
		}
		if _, err := opts.DB.Exec(`INSERT INTO guardians_children (guardian_user_id, child_user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, uid, body.ChildUserID); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// --- Progress ---
	app.Get("/v1/progress/overview", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		userID := c.Query("user_id", uid)
		if userID != uid {
			var ok bool
			if err := opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM guardians_children WHERE guardian_user_id=$1 AND child_user_id=$2)`, uid, userID); err != nil {
				return fiber.ErrForbidden
			}
			if !ok {
				return fiber.ErrForbidden
			}
		}
		var classes int
		_ = opts.DB.Get(&classes, `SELECT COUNT(*) FROM class_enrollments WHERE student_user_id=$1`, userID)
		var completed int
		_ = opts.DB.Get(&completed, `SELECT COUNT(*) FROM lesson_progress WHERE student_user_id=$1 AND status='completed'`, userID)
		var attempted int
		_ = opts.DB.Get(&attempted, `SELECT COUNT(*) FROM test_attempts WHERE student_user_id=$1 AND status<>'in_progress'`, userID)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"classesEnrolled": classes, "lessonsCompleted": completed, "testsAttempted": attempted}})
	})
	// List lesson progress for current user (optionally guardian viewing a child)
	app.Get("/v1/progress/lessons", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		userID := c.Query("user_id", uid)
		if userID != uid {
			var ok bool
			if err := opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM guardians_children WHERE guardian_user_id=$1 AND child_user_id=$2)`, uid, userID); err != nil {
				return fiber.ErrForbidden
			}
			if !ok {
				return fiber.ErrForbidden
			}
		}
		status := c.Query("status")
		if status != "" && status != "in_progress" && status != "completed" {
			return fiber.ErrBadRequest
		}
		type row struct {
			LessonID string `json:"lessonId" db:"lesson_id"`
			Status   string `json:"status" db:"status"`
		}
		rows := []row{}
		var q string
		if status == "" {
			q = `SELECT lesson_id, status FROM lesson_progress WHERE student_user_id=$1 ORDER BY updated_at DESC LIMIT 100`
			if err := opts.DB.Select(&rows, q, userID); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		} else {
			q = `SELECT lesson_id, status FROM lesson_progress WHERE student_user_id=$1 AND status=$2 ORDER BY updated_at DESC LIMIT 100`
			if err := opts.DB.Select(&rows, q, userID, status); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	// Teacher analytics
	app.Get("/v1/analytics/teacher", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var classes, students, graded int
		_ = opts.DB.Get(&classes, `SELECT COUNT(*) FROM classes WHERE tutor_user_id=$1 AND status='active'`, uid)
		_ = opts.DB.Get(&students, `SELECT COUNT(DISTINCT e.student_user_id) FROM class_enrollments e JOIN classes c ON c.id=e.class_id WHERE c.tutor_user_id=$1`, uid)
		_ = opts.DB.Get(&graded, `SELECT COUNT(*) FROM test_attempts ta JOIN tests t ON t.id=ta.test_id JOIN classes c ON c.id=t.class_id WHERE c.tutor_user_id=$1 AND ta.status='graded'`, uid)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"classes": classes, "students": students, "gradedAttempts": graded}})
	})
	// School analytics
	app.Get("/v1/analytics/school/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var classes, students int
		_ = opts.DB.Get(&classes, `SELECT COUNT(*) FROM classes WHERE school_id=$1 AND status='active'`, sid)
		_ = opts.DB.Get(&students, `SELECT COUNT(DISTINCT e.student_user_id) FROM class_enrollments e JOIN classes c ON c.id=e.class_id WHERE c.school_id=$1`, sid)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"classes": classes, "students": students}})
	})
	// --- School Staff Management ---
	app.Get("/v1/schools/:id/members", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		okA, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !okA {
			return fiber.ErrForbidden
		}
		type row struct {
			ID        string    `json:"id" db:"id"`
			UserID    string    `json:"userId" db:"user_id"`
			Role      string    `json:"role" db:"role"`
			Status    string    `json:"status" db:"status"`
			CreatedAt time.Time `json:"createdAt" db:"created_at"`
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT id, user_id, role, status, created_at FROM school_members WHERE school_id=$1 ORDER BY created_at DESC`, sid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	// --- Gradebook ---
	// Class gradebook: tutor/school admin only
	app.Get("/v1/classes/:id/gradebook", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		classID := c.Params("id")
		// Permissions
		allowed, _ := auth.IsClassTutor(opts.DB, classID, uid)
		if !allowed {
			var sid *string
			_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, classID)
			if sid != nil && *sid != "" {
				allowed, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
			}
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		// Students in class
		type studentRow struct {
			UserID      string  `db:"student_user_id"`
			DisplayName *string `db:"display_name"`
		}
		students := []studentRow{}
		if err := opts.DB.Select(&students, `SELECT e.student_user_id, up.display_name
			FROM class_enrollments e LEFT JOIN user_profiles up ON up.user_id=e.student_user_id
			WHERE e.class_id=$1 AND e.status='active' ORDER BY up.display_name NULLS LAST, e.student_user_id`, classID); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		// Tests linked to class via subjects/lessons
		type testRow struct {
			ID        string    `db:"id"`
			Title     string    `db:"title"`
			CreatedAt time.Time `db:"created_at"`
		}
		tests := []testRow{}
		if err := opts.DB.Select(&tests, `
			SELECT t.id, t.title, t.created_at FROM tests t
			LEFT JOIN subjects s ON s.id=t.subject_id
			LEFT JOIN lessons l ON l.id=t.lesson_id
			LEFT JOIN subjects s2 ON s2.id=l.subject_id
			LEFT JOIN classes c ON c.id=s.class_id
			LEFT JOIN classes c2 ON c2.id=s2.class_id
			WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted'
			ORDER BY t.created_at ASC`, classID); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		// Max points per test
		maxByTest := map[string]float64{}
		for _, tr := range tests {
			var max float64
			_ = opts.DB.Get(&max, `SELECT COALESCE(SUM(points),0) FROM test_questions WHERE test_id=$1`, tr.ID)
			maxByTest[tr.ID] = max
		}
		// Attempts for all students across tests
		attempts := []struct {
			TestID  string          `db:"test_id"`
			UserID  string          `db:"student_user_id"`
			Score   *float64        `db:"score"`
			Status  string          `db:"status"`
			At      *time.Time      `db:"submitted_at"`
			Grading json.RawMessage `db:"grading"`
		}{}
		if len(tests) > 0 && len(students) > 0 {
			testIDs := make([]string, 0, len(tests))
			for _, t := range tests {
				testIDs = append(testIDs, t.ID)
			}
			studentIDs := make([]string, 0, len(students))
			for _, s := range students {
				studentIDs = append(studentIDs, s.UserID)
			}
			q, args, _ := sqlx.In(`SELECT test_id, student_user_id, score, status, submitted_at, grading FROM test_attempts WHERE test_id IN (?) AND student_user_id IN (?)`, testIDs, studentIDs)
			q = opts.DB.Rebind(q)
			if err := opts.DB.Select(&attempts, q, args...); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}
		// Assemble map student->test->grade
		type grade struct {
			Score       *float64   `json:"score"`
			Earned      *float64   `json:"earned"`
			Max         float64    `json:"max"`
			Status      string     `json:"status"`
			SubmittedAt *time.Time `json:"submittedAt"`
		}
		grades := map[string]map[string]grade{}
		for _, s := range students {
			grades[s.UserID] = map[string]grade{}
		}
		for _, a := range attempts {
			max := maxByTest[a.TestID]
			var earned *float64
			if len(a.Grading) > 0 && string(a.Grading) != "null" {
				var g map[string]any
				_ = json.Unmarshal(a.Grading, &g)
				if v, ok := g["earned"].(float64); ok {
					earned = &v
				}
				// if grading.max missing, fallback to schema max
			}
			if earned == nil && a.Score != nil && max > 0 {
				v := (*a.Score / 100.0) * max
				earned = &v
			}
			if _, ok := grades[a.UserID]; !ok {
				grades[a.UserID] = map[string]grade{}
			}
			grades[a.UserID][a.TestID] = grade{Score: a.Score, Earned: earned, Max: max, Status: a.Status, SubmittedAt: a.At}
		}
		// Compute totals per student
		// Find school for class and active grading scale
		var schoolID *string
		_ = opts.DB.Get(&schoolID, `SELECT school_id FROM classes WHERE id=$1`, classID)
		type entry struct {
			Min, Max float64
			Letter   string
		}
		entries := []entry{}
		if schoolID != nil && *schoolID != "" {
			_ = opts.DB.Select(&entries, `SELECT e.min_percent AS min, e.max_percent AS max, e.letter FROM grading_scale_entries e JOIN grading_scales s ON s.id=e.scale_id WHERE s.school_id=$1 AND s.active=true AND s.is_default=true ORDER BY e.min_percent DESC`, *schoolID)
		}
		letterFor := func(pct *float64) *string {
			if pct == nil || len(entries) == 0 {
				return nil
			}
			for _, e := range entries {
				if *pct+1e-9 >= e.Min && *pct <= e.Max+1e-9 {
					v := e.Letter
					return &v
				}
			}
			return nil
		}

		rows := []fiber.Map{}
		for _, s := range students {
			totalEarned := 0.0
			totalMax := 0.0
			gmap := grades[s.UserID]
			for _, tr := range tests {
				m := maxByTest[tr.ID]
				totalMax += m
				if g, ok := gmap[tr.ID]; ok && g.Earned != nil {
					totalEarned += *g.Earned
				}
			}
			var percent *float64
			if totalMax > 0 {
				p := (totalEarned / totalMax) * 100.0
				percent = &p
			}
			name := s.UserID
			if s.DisplayName != nil && strings.TrimSpace(*s.DisplayName) != "" {
				name = *s.DisplayName
			}
			rows = append(rows, fiber.Map{"userId": s.UserID, "name": name, "earned": totalEarned, "max": totalMax, "percent": percent, "letter": letterFor(percent), "grades": gmap})
		}
		// tests summary
		testSumm := []fiber.Map{}
		for _, tr := range tests {
			testSumm = append(testSumm, fiber.Map{"id": tr.ID, "title": tr.Title, "max": maxByTest[tr.ID]})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"tests": testSumm, "students": rows}})
	})
	// CSV export
	app.Get("/v1/classes/:id/gradebook.csv", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/csv; charset=utf-8")
		c.Set("Content-Disposition", "attachment; filename=gradebook.csv")
		// Build CSV from the same data as JSON endpoint
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		classID := c.Params("id")
		allowed, _ := auth.IsClassTutor(opts.DB, classID, uid)
		if !allowed {
			var sid *string
			_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, classID)
			if sid != nil && *sid != "" {
				allowed, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
			}
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		// fetch json pieces
		type TR struct {
			ID, Title string
			Max       float64
		}
		tests := []TR{}
		_ = opts.DB.Select(&tests, `SELECT t.id, t.title, COALESCE((SELECT SUM(points) FROM test_questions WHERE test_id=t.id),0) AS max
			FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted' ORDER BY t.created_at ASC`, classID)
		students := []struct{ UserID, Name string }{}
		_ = opts.DB.Select(&students, `SELECT e.student_user_id AS user_id, COALESCE(NULLIF(TRIM(up.display_name),''), e.student_user_id) AS name FROM class_enrollments e LEFT JOIN user_profiles up ON up.user_id=e.student_user_id WHERE e.class_id=$1 AND e.status='active' ORDER BY name`, classID)
		// attempts map
		attempts := []struct {
			TestID, UserID string
			Score          *float64
			Grading        json.RawMessage
		}{}
		if len(tests) > 0 && len(students) > 0 {
			tids := make([]string, 0, len(tests))
			for _, t := range tests {
				tids = append(tids, t.ID)
			}
			uids := make([]string, 0, len(students))
			for _, s := range students {
				uids = append(uids, s.UserID)
			}
			q, args, _ := sqlx.In(`SELECT test_id, student_user_id, score, grading FROM test_attempts WHERE test_id IN (?) AND student_user_id IN (?)`, tids, uids)
			q = opts.DB.Rebind(q)
			_ = opts.DB.Select(&attempts, q, args...)
		}
		// build map for quick lookup
		am := map[string]map[string]struct {
			Score  *float64
			Earned *float64
		}{}
		for _, a := range attempts {
			if am[a.UserID] == nil {
				am[a.UserID] = map[string]struct {
					Score  *float64
					Earned *float64
				}{}
			}
			var earned *float64
			if len(a.Grading) > 0 && string(a.Grading) != "null" {
				var g map[string]any
				_ = json.Unmarshal(a.Grading, &g)
				if v, ok := g["earned"].(float64); ok {
					earned = &v
				}
			}
			am[a.UserID][a.TestID] = struct {
				Score  *float64
				Earned *float64
			}{a.Score, earned}
		}
		// write CSV
		var b strings.Builder
		b.WriteString("Student")
		for _, t := range tests {
			b.WriteString("," + strings.ReplaceAll(t.Title, ",", " "))
		}
		b.WriteString(",Total %\n")
		for _, s := range students {
			b.WriteString(s.Name)
			totalEarned := 0.0
			totalMax := 0.0
			for _, t := range tests {
				var cell string
				if a, ok := am[s.UserID][t.ID]; ok {
					if a.Score != nil {
						cell = fmt.Sprintf("%.2f", *a.Score)
					} else if a.Earned != nil && t.Max > 0 {
						cell = fmt.Sprintf("%.2f", (*a.Earned/t.Max)*100.0)
					}
					if a.Earned != nil {
						totalEarned += *a.Earned
					}
				}
				totalMax += t.Max
				if cell == "" {
					cell = ""
				}
				b.WriteString("," + cell)
			}
			var pct string
			if totalMax > 0 {
				pct = fmt.Sprintf("%.2f", (totalEarned/totalMax)*100.0)
			}
			b.WriteString("," + pct + "\n")
		}
		return c.SendString(b.String())
	})
	// --- Transcripts ---
	// Helper: permission to view user transcript
	canViewUser := func(viewerID, targetID, schoolID string) bool {
		if viewerID == targetID {
			return true
		}
		// guardian
		var ok bool
		_ = opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM guardians_children WHERE guardian_user_id=$1 AND child_user_id=$2)`, viewerID, targetID)
		if ok {
			return true
		}
		// platform admin
		if isPlatformAdmin(viewerID) {
			return true
		}
		// school admin scoped to schoolID if provided
		if strings.TrimSpace(schoolID) != "" {
			ok, _ = auth.IsSchoolAdmin(opts.DB, schoolID, viewerID)
			if ok {
				return true
			}
		}
		return false
	}
	// JSON transcript for a user (overall or filtered by school)
	app.Get("/v1/transcripts/:userId", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		viewer, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		target := c.Params("userId")
		if target == "me" {
			target = viewer
		}
		sid := strings.TrimSpace(c.Query("school_id"))
		if !canViewUser(viewer, target, sid) {
			return fiber.ErrForbidden
		}
		// Classes for target (optionally by school)
		type classRow struct {
			ID, Title string
			SchoolID  *string
		}
		classes := []classRow{}
		if sid == "" {
			_ = opts.DB.Select(&classes, `SELECT c.id, c.title, c.school_id FROM classes c JOIN class_enrollments e ON e.class_id=c.id WHERE e.student_user_id=$1 AND e.status='active' ORDER BY c.title`, target)
		} else {
			_ = opts.DB.Select(&classes, `SELECT c.id, c.title, c.school_id FROM classes c JOIN class_enrollments e ON e.class_id=c.id WHERE e.student_user_id=$1 AND e.status='active' AND c.school_id=$2 ORDER BY c.title`, target, sid)
		}
		// For each class, compute earned/max similarly to gradebook but for single user
		out := []fiber.Map{}
		for _, cr := range classes {
			// tests under class
			tests := []struct{ ID, Title string }{}
			_ = opts.DB.Select(&tests, `SELECT t.id, t.title FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted' ORDER BY t.created_at`, cr.ID)
			maxBy := map[string]float64{}
			for _, t := range tests {
				var m float64
				_ = opts.DB.Get(&m, `SELECT COALESCE(SUM(points),0) FROM test_questions WHERE test_id=$1`, t.ID)
				maxBy[t.ID] = m
			}
			attempts := []struct {
				TestID  string
				Score   *float64
				Grading json.RawMessage
			}{}
			if len(tests) > 0 {
				tids := make([]string, 0, len(tests))
				for _, t := range tests {
					tids = append(tids, t.ID)
				}
				q, args, _ := sqlx.In(`SELECT test_id, score, grading FROM test_attempts WHERE student_user_id=$1 AND test_id IN (?)`, target, tids)
				q = opts.DB.Rebind(q)
				_ = opts.DB.Select(&attempts, q, args...)
			}
			earned := 0.0
			max := 0.0
			for _, t := range tests {
				max += maxBy[t.ID]
			}
			for _, a := range attempts {
				if len(a.Grading) > 0 && string(a.Grading) != "null" {
					var g map[string]any
					_ = json.Unmarshal(a.Grading, &g)
					if v, ok := g["earned"].(float64); ok {
						earned += v
					}
				} else if a.Score != nil {
					if mx := maxBy[a.TestID]; mx > 0 {
						earned += (*a.Score / 100.0) * mx
					}
				}
			}
			var percent *float64
			if max > 0 {
				p := (earned / max) * 100.0
				percent = &p
			}
			out = append(out, fiber.Map{"classId": cr.ID, "title": cr.Title, "earned": earned, "max": max, "percent": percent})
		}
		// overall summary
		totalE, totalM := 0.0, 0.0
		for _, r := range out {
			totalE += r["earned"].(float64)
			totalM += r["max"].(float64)
		}
		var overall *float64
		if totalM > 0 {
			v := (totalE / totalM) * 100.0
			overall = &v
		}
		// student display
		var name string
		_ = opts.DB.Get(&name, `SELECT COALESCE(NULLIF(TRIM(display_name),''), $1) FROM user_profiles WHERE user_id=$1`, target)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"userId": target, "name": name, "overallPercent": overall, "classes": out}})
	})
	// PDF transcript
	app.Get("/v1/transcripts/:userId.pdf", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		viewer, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		target := c.Params("userId")
		if target == "me" {
			target = viewer
		}
		sid := strings.TrimSpace(c.Query("school_id"))
		if !canViewUser(viewer, target, sid) {
			return fiber.ErrForbidden
		}
		// reuse JSON handler by querying DB directly (same as above, condensed)
		var name string
		_ = opts.DB.Get(&name, `SELECT COALESCE(NULLIF(TRIM(display_name),''), $1) FROM user_profiles WHERE user_id=$1`, target)
		classes := []struct{ ID, Title string }{}
		if sid == "" {
			_ = opts.DB.Select(&classes, `SELECT c.id, c.title FROM classes c JOIN class_enrollments e ON e.class_id=c.id WHERE e.student_user_id=$1 AND e.status='active' ORDER BY c.title`, target)
		} else {
			_ = opts.DB.Select(&classes, `SELECT c.id, c.title FROM classes c JOIN class_enrollments e ON e.class_id=c.id WHERE e.student_user_id=$1 AND e.status='active' AND c.school_id=$2 ORDER BY c.title`, target, sid)
		}
		rows := []struct {
			Title   string
			Percent *float64
		}{}
		for _, cr := range classes {
			// compute class percent
			tests := []struct{ ID string }{}
			_ = opts.DB.Select(&tests, `SELECT t.id FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted'`, cr.ID)
			mx := 0.0
			earned := 0.0
			for _, t := range tests {
				var m float64
				_ = opts.DB.Get(&m, `SELECT COALESCE(SUM(points),0) FROM test_questions WHERE test_id=$1`, t.ID)
				mx += m
				var sc *float64
				var gr json.RawMessage
				_ = opts.DB.Get(&sc, `SELECT score FROM test_attempts WHERE student_user_id=$1 AND test_id=$2`, target, t.ID)
				_ = opts.DB.Get(&gr, `SELECT grading FROM test_attempts WHERE student_user_id=$1 AND test_id=$2`, target, t.ID)
				if len(gr) > 0 && string(gr) != "null" {
					var g map[string]any
					_ = json.Unmarshal(gr, &g)
					if v, ok := g["earned"].(float64); ok {
						earned += v
					}
				} else if sc != nil && m > 0 {
					earned += (*sc / 100.0) * m
				}
			}
			var pct *float64
			if mx > 0 {
				v := (earned / mx) * 100.0
				pct = &v
			}
			rows = append(rows, struct {
				Title   string
				Percent *float64
			}{cr.Title, pct})
		}
		// render PDF
		pdf := gofpdf.New("P", "mm", "A4", "")
		pdf.SetTitle("Transcript", false)
		pdf.AddPage()
		pdf.SetFont("Helvetica", "B", 16)
		pdf.Cell(0, 10, "Academic Transcript")
		pdf.Ln(12)
		pdf.SetFont("Helvetica", "", 12)
		pdf.Cell(0, 8, fmt.Sprintf("Student: %s (%s)", name, target))
		pdf.Ln(10)
		pdf.SetFont("Helvetica", "B", 11)
		pdf.CellFormat(120, 8, "Class", "B", 0, "L", false, 0, "")
		pdf.CellFormat(40, 8, "Percent", "B", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 11)
		for _, r := range rows {
			pdf.CellFormat(120, 7, r.Title, "", 0, "L", false, 0, "")
			var pctStr string
			if r.Percent != nil {
				pctStr = fmt.Sprintf("%.2f%%", *r.Percent)
			} else {
				pctStr = "—"
			}
			pdf.CellFormat(40, 7, pctStr, "", 1, "L", false, 0, "")
		}
		var buf bytes.Buffer
		if err := pdf.Output(&buf); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		c.Set("Content-Type", "application/pdf")
		c.Set("Content-Disposition", "inline; filename=transcript.pdf")
		return c.Send(buf.Bytes())
	})
	// --- Certificates ---
	genCode := func() string {
		r := strings.ToUpper(strings.ReplaceAll(uuid.New().String(), "-", ""))
		if len(r) > 12 {
			r = r[:12]
		}
		return r
	}
	// Create a certificate template
	app.Post("/v1/certificates/templates", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			Scope    string          `json:"scope"` // 'platform' or 'school'
			SchoolID string          `json:"schoolId"`
			Name     string          `json:"name"`
			Body     json.RawMessage `json:"body"`
			Style    json.RawMessage `json:"style"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		scope := strings.ToLower(strings.TrimSpace(body.Scope))
		if scope != "platform" && scope != "school" {
			return fiber.ErrBadRequest
		}
		if scope == "platform" {
			if !isPlatformAdmin(uid) {
				return fiber.ErrForbidden
			}
		}
		var schoolID *string
		if scope == "school" {
			s := strings.TrimSpace(body.SchoolID)
			if s == "" {
				return fiber.ErrBadRequest
			}
			ok, _ := auth.IsSchoolAdmin(opts.DB, s, uid)
			if !ok {
				return fiber.ErrForbidden
			}
			schoolID = &s
		}
		var out struct {
			ID       string  `json:"id"`
			Name     string  `json:"name"`
			Scope    string  `json:"scope"`
			SchoolID *string `json:"schoolId"`
		}
		if err := opts.DB.Get(&out, `INSERT INTO certificate_templates (scope, school_id, name, body, style, created_by_user_id) VALUES ($1,$2,$3,$4,$5,$6)
			RETURNING id, name, scope, school_id`, scope, schoolID, strings.TrimSpace(body.Name), nullIfEmptyJSON(body.Body), nullIfEmptyJSON(body.Style), uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})
	// List templates (platform admin sees platform; school admin sees their school)
	app.Get("/v1/certificates/templates", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		scope := strings.TrimSpace(strings.ToLower(c.Query("scope")))
		sid := strings.TrimSpace(c.Query("school_id"))
		type row struct {
			ID, Name, Scope string
			SchoolID        *string `db:"school_id"`
		}
		rows := []row{}
		if scope == "platform" || (scope == "" && isPlatformAdmin(uid)) {
			if !isPlatformAdmin(uid) {
				return fiber.ErrForbidden
			}
			_ = opts.DB.Select(&rows, `SELECT id, name, scope, school_id FROM certificate_templates WHERE scope='platform' AND status='active' ORDER BY created_at DESC`)
			return c.JSON(fiber.Map{"success": true, "data": rows})
		}
		if sid == "" {
			return fiber.ErrBadRequest
		}
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		_ = opts.DB.Select(&rows, `SELECT id, name, scope, school_id FROM certificate_templates WHERE scope='school' AND school_id=$1 AND status='active' ORDER BY created_at DESC`, sid)
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	// Issue a certificate
	app.Post("/v1/certificates/issue", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			TemplateID string          `json:"templateId"`
			Recipient  string          `json:"recipientUserId"`
			ClassID    string          `json:"classId"`
			Data       json.RawMessage `json:"data"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		if strings.TrimSpace(body.TemplateID) == "" || strings.TrimSpace(body.Recipient) == "" {
			return fiber.ErrBadRequest
		}
		// Load template and check permission
		var tpl struct {
			ID, Scope string
			SchoolID  *string `db:"school_id"`
		}
		if err := opts.DB.Get(&tpl, `SELECT id, scope, school_id FROM certificate_templates WHERE id=$1 AND status='active'`, body.TemplateID); err != nil {
			return fiber.ErrBadRequest
		}
		if tpl.Scope == "platform" {
			if !isPlatformAdmin(uid) {
				return fiber.ErrForbidden
			}
		} else {
			if tpl.SchoolID == nil || *tpl.SchoolID == "" {
				return fiber.ErrBadRequest
			}
			ok, _ := auth.IsSchoolAdmin(opts.DB, *tpl.SchoolID, uid)
			if !ok {
				// allow class tutor if class belongs to same school and provided
				if strings.TrimSpace(body.ClassID) == "" {
					return fiber.ErrForbidden
				}
				ok, _ = auth.IsClassTutor(opts.DB, body.ClassID, uid)
				if !ok {
					return fiber.ErrForbidden
				}
			}
		}
		code := genCode()
		verifyURL := strings.TrimRight(opts.CoreAPIBase, "/") // could be public URL; reuse core base as placeholder
		// optional signature
		sig := ""
		alg := ""
		if certSigner != nil {
			payload := map[string]any{"templateId": body.TemplateID, "recipientUserId": body.Recipient, "classId": strings.TrimSpace(body.ClassID), "code": code}
			pb, _ := json.Marshal(payload)
			h := sha256.Sum256(pb)
			sig = base64.StdEncoding.EncodeToString(ed25519.Sign(certSigner, h[:]))
			alg = certSignerAlg
		}
		var out struct{ ID, Code string }
		if err := opts.DB.Get(&out, `INSERT INTO certificates (template_id, recipient_user_id, class_id, data, code, issued_by_user_id, verify_url, signature, signature_alg)
	            VALUES ($1,$2, NULLIF($3,''), $4, $5, $6, $7, NULLIF($8,''), NULLIF($9,'')) RETURNING id, code`, body.TemplateID, body.Recipient, body.ClassID, nullIfEmptyJSON(body.Data), code, uid, verifyURL, sig, alg); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		logAudit(uid, "certificate_issue", "certificate", out.ID, fiber.Map{"recipient": body.Recipient, "code": out.Code})
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})
	// Revoke a certificate
	app.Post("/v1/certificates/:id/revoke", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		// owner permission: platform or school admin for the template scope
		var tpl struct {
			Scope    string
			SchoolID *string `db:"school_id"`
		}
		if err := opts.DB.Get(&tpl, `SELECT t.scope, t.school_id FROM certificates ce JOIN certificate_templates t ON t.id=ce.template_id WHERE ce.id=$1`, id); err != nil {
			return fiber.ErrBadRequest
		}
		if tpl.Scope == "platform" {
			if !isPlatformAdmin(uid) {
				return fiber.ErrForbidden
			}
		} else {
			if tpl.SchoolID == nil || *tpl.SchoolID == "" {
				return fiber.ErrBadRequest
			}
			ok, _ := auth.IsSchoolAdmin(opts.DB, *tpl.SchoolID, uid)
			if !ok {
				return fiber.ErrForbidden
			}
		}
		_, err = opts.DB.Exec(`UPDATE certificates SET status='revoked', revoked_at=now() WHERE id=$1 AND status<>'revoked'`, id)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		logAudit(uid, "certificate_revoke", "certificate", id, nil)
		return c.JSON(fiber.Map{"success": true})
	})
	// Verify endpoint by code
	app.Get("/v1/certificates/verify", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		code := strings.TrimSpace(c.Query("code"))
		if code == "" {
			return fiber.ErrBadRequest
		}
		var row struct {
			ID, TemplateName string
			Status           string
			Recipient        string
			IssuedAt         time.Time
			Signature        *string `db:"signature"`
			SignatureAlg     *string `db:"signature_alg"`
		}
		if err := opts.DB.Get(&row, `SELECT ce.id, tp.name AS template_name, ce.status, ce.recipient_user_id AS recipient, ce.issued_at, ce.signature, ce.signature_alg
            FROM certificates ce JOIN certificate_templates tp ON tp.id=ce.template_id WHERE ce.code=$1`, code); err != nil {
			return fiber.ErrNotFound
		}
		var display string
		_ = opts.DB.Get(&display, `SELECT COALESCE(NULLIF(TRIM(display_name),''), $1) FROM user_profiles WHERE user_id=$1`, row.Recipient)
		verified := false
		if pkb64 := strings.TrimSpace(os.Getenv("CERT_PUBLIC_KEY")); pkb64 != "" && row.Signature != nil && row.SignatureAlg != nil && *row.SignatureAlg == "ed25519-sha256" {
			if pkraw, err := base64.StdEncoding.DecodeString(pkb64); err == nil && len(pkraw) == ed25519.PublicKeySize {
				var payload struct {
					TemplateName, Recipient string
					IssuedAt                time.Time
					Code                    string
				}
				payload.TemplateName = row.TemplateName
				payload.Recipient = row.Recipient
				payload.IssuedAt = row.IssuedAt
				payload.Code = code
				pb, _ := json.Marshal(payload)
				h := sha256.Sum256(pb)
				sigraw, _ := base64.StdEncoding.DecodeString(*row.Signature)
				verified = ed25519.Verify(ed25519.PublicKey(pkraw), h[:], sigraw)
			}
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": row.ID, "template": row.TemplateName, "status": row.Status, "recipient": fiber.Map{"userId": row.Recipient, "name": display}, "issuedAt": row.IssuedAt, "verified": verified}})
	})
	// Certificate PDF by ID (public if you have the code param)
	app.Get("/v1/certificates/:id.pdf", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		id := c.Params("id")
		code := strings.TrimSpace(c.Query("code"))
		var row struct {
			ID, Code, TemplateName string
			Status                 string
			Recipient              string
			IssuedAt               time.Time
		}
		if err := opts.DB.Get(&row, `SELECT ce.id, ce.code, tp.name AS template_name, ce.status, ce.recipient_user_id AS recipient, ce.issued_at FROM certificates ce JOIN certificate_templates tp ON tp.id=ce.template_id WHERE ce.id=$1`, id); err != nil {
			return fiber.ErrNotFound
		}
		// If not provided correct code and not authenticated authorized viewer, disallow when revoked
		if code != row.Code {
			// require auth and ownership
			uid, err := getUserID(c)
			if err != nil {
				return fiber.ErrUnauthorized
			}
			if uid != row.Recipient && !isPlatformAdmin(uid) {
				return fiber.ErrForbidden
			}
		}
		var name string
		_ = opts.DB.Get(&name, `SELECT COALESCE(NULLIF(TRIM(display_name),''), $1) FROM user_profiles WHERE user_id=$1`, row.Recipient)
		pdf := gofpdf.New("L", "mm", "A4", "")
		pdf.AddPage()
		pdf.SetFont("Helvetica", "B", 28)
		pdf.Cell(0, 18, row.TemplateName)
		pdf.Ln(20)
		pdf.SetFont("Helvetica", "", 16)
		pdf.Cell(0, 10, fmt.Sprintf("Awarded to %s", name))
		pdf.Ln(12)
		pdf.Cell(0, 8, fmt.Sprintf("Issued: %s", row.IssuedAt.Format("2006-01-02")))
		pdf.Ln(10)
		pdf.SetFont("Helvetica", "I", 11)
		pdf.Cell(0, 7, fmt.Sprintf("Certificate Code: %s", row.Code))
		pdf.Ln(8)
		pdf.Cell(0, 7, "Verify at: /v1/certificates/verify?code="+row.Code)
		var buf bytes.Buffer
		if err := pdf.Output(&buf); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		c.Set("Content-Type", "application/pdf")
		c.Set("Content-Disposition", "inline; filename=certificate.pdf")
		return c.Send(buf.Bytes())
	})

	// --- Discussions & Announcements ---
	// Check if user is class member (enrolled) or tutor/admin
	isClassMember := func(classID, userID string) bool {
		if classID == "" || userID == "" {
			return false
		}
		var ok bool
		_ = opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM class_enrollments WHERE class_id=$1 AND student_user_id=$2 AND status='active')`, classID, userID)
		if ok {
			return true
		}
		ok, _ = auth.IsClassTutor(opts.DB, classID, userID)
		if ok {
			return true
		}
		var sid *string
		_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, classID)
		if sid != nil && *sid != "" {
			ok, _ = auth.IsSchoolAdmin(opts.DB, *sid, userID)
			if ok {
				return true
			}
		}
		return false
	}

	// Discussions
	app.Get("/v1/classes/:id/discussions", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		cid := c.Params("id")
		if !isClassMember(cid, uid) {
			return fiber.ErrForbidden
		}
		type row struct {
			ID, Title, CreatedBy string
			CreatedAt            time.Time `db:"created_at"`
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT id, title, created_by_user_id AS created_by, created_at FROM class_discussions WHERE class_id=$1 ORDER BY created_at DESC LIMIT 200`, cid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/classes/:id/discussions", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		cid := c.Params("id")
		if !isClassMember(cid, uid) {
			return fiber.ErrForbidden
		}
		var body struct {
			Title string `json:"title"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Title) == "" {
			return fiber.ErrBadRequest
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := opts.DB.Get(&out, `INSERT INTO class_discussions (class_id, title, created_by_user_id) VALUES ($1,$2,$3) RETURNING id`, cid, strings.TrimSpace(body.Title), uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})
	app.Get("/v1/discussions/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		did := c.Params("id")
		var cid string
		if err := opts.DB.Get(&cid, `SELECT class_id FROM class_discussions WHERE id=$1`, did); err != nil {
			return fiber.ErrNotFound
		}
		if !isClassMember(cid, uid) {
			return fiber.ErrForbidden
		}
		var head struct {
			ID, Title string
			CreatedBy string
			CreatedAt time.Time
		}
		_ = opts.DB.Get(&head, `SELECT id, title, created_by_user_id AS created_by, created_at FROM class_discussions WHERE id=$1`, did)
		type post struct {
			ID, UserID, Body string
			CreatedAt        time.Time
			Attachments      json.RawMessage
		}
		posts := []post{}
		_ = opts.DB.Select(&posts, `SELECT id, user_id, body, created_at, COALESCE(attachments,'null'::jsonb) AS attachments FROM class_posts WHERE discussion_id=$1 ORDER BY created_at ASC`, did)
		// moderation: class tutor or school admin
		canMod, _ := auth.IsClassTutor(opts.DB, cid, uid)
		if !canMod {
			var sid *string
			_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, cid)
			if sid != nil && *sid != "" {
				canMod, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
			}
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"discussion": head, "posts": posts, "canModerate": canMod}})
	})
	app.Post("/v1/discussions/:id/posts", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		did := c.Params("id")
		var cid string
		if err := opts.DB.Get(&cid, `SELECT class_id FROM class_discussions WHERE id=$1`, did); err != nil {
			return fiber.ErrNotFound
		}
		if !isClassMember(cid, uid) {
			return fiber.ErrForbidden
		}
		var body struct {
			Body        string          `json:"body"`
			Attachments json.RawMessage `json:"attachments"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Body) == "" {
			return fiber.ErrBadRequest
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := opts.DB.Get(&out, `INSERT INTO class_posts (discussion_id, user_id, body, attachments) VALUES ($1,$2,$3,$4) RETURNING id`, did, uid, strings.TrimSpace(body.Body), nullIfEmptyJSON(body.Attachments)); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})

	// Announcements
	app.Get("/v1/classes/:id/announcements", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		cid := c.Params("id")
		if !isClassMember(cid, uid) {
			return fiber.ErrForbidden
		}
		type row struct {
			ID, Title, Body, CreatedBy string
			Pinned                     bool
			PublishedAt                time.Time
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT id, title, COALESCE(body,'') AS body, created_by_user_id AS created_by, pinned, published_at FROM class_announcements WHERE class_id=$1 ORDER BY pinned DESC, published_at DESC LIMIT 200`, cid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/classes/:id/announcements", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		cid := c.Params("id")
		allowed, _ := auth.IsClassTutor(opts.DB, cid, uid)
		if !allowed {
			var sid *string
			_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, cid)
			if sid != nil && *sid != "" {
				allowed, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
			}
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct {
			Title  string  `json:"title"`
			Body   *string `json:"body"`
			Pinned *bool   `json:"pinned"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Title) == "" {
			return fiber.ErrBadRequest
		}
		pinned := false
		if body.Pinned != nil {
			pinned = *body.Pinned
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := opts.DB.Get(&out, `INSERT INTO class_announcements (class_id, title, body, pinned, created_by_user_id) VALUES ($1,$2,$3,$4,$5) RETURNING id`, cid, strings.TrimSpace(body.Title), body.Body, pinned, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		studentIDs := []string{}
		_ = opts.DB.Select(&studentIDs, `SELECT student_user_id FROM class_enrollments WHERE class_id=$1 AND status='active'`, cid)
		for _, sid := range studentIDs {
			nb, _ := json.Marshal(fiber.Map{"type": "class_announcement", "classId": cid, "announcementId": out.ID})
			_, _ = opts.DB.Exec(`INSERT INTO notifications_queue (recipient_user_id, ntype, payload) VALUES ($1,$2,$3)`, sid, "class_announcement", nb)
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})

	// Certificate Rules
	app.Post("/v1/certificates/rules", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			Scope      string          `json:"scope"`
			SchoolID   string          `json:"schoolId"`
			ClassID    string          `json:"classId"`
			TemplateID string          `json:"templateId"`
			Enabled    bool            `json:"enabled"`
			Conditions json.RawMessage `json:"conditions"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		scope := strings.ToLower(strings.TrimSpace(body.Scope))
		if scope != "class" && scope != "school" {
			return fiber.ErrBadRequest
		}
		if strings.TrimSpace(body.TemplateID) == "" {
			return fiber.ErrBadRequest
		}
		var sid *string
		var cid *string
		if scope == "school" {
			if strings.TrimSpace(body.SchoolID) == "" {
				return fiber.ErrBadRequest
			}
			s := strings.TrimSpace(body.SchoolID)
			sid = &s
			ok, _ := auth.IsSchoolAdmin(opts.DB, s, uid)
			if !ok {
				return fiber.ErrForbidden
			}
		} else {
			if strings.TrimSpace(body.ClassID) == "" {
				return fiber.ErrBadRequest
			}
			s := strings.TrimSpace(body.ClassID)
			cid = &s
			// allow class tutor or school admin where applicable
			var tutor string
			_ = opts.DB.Get(&tutor, `SELECT tutor_user_id FROM classes WHERE id=$1`, s)
			allowed := tutor == uid
			if !allowed {
				var sch *string
				_ = opts.DB.Get(&sch, `SELECT school_id FROM classes WHERE id=$1`, s)
				if sch != nil && *sch != "" {
					allowed, _ = auth.IsSchoolAdmin(opts.DB, *sch, uid)
				}
			}
			if !allowed {
				return fiber.ErrForbidden
			}
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := opts.DB.Get(&out, `INSERT INTO certificate_rules (scope, school_id, class_id, template_id, enabled, conditions, created_by_user_id) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, scope, sid, cid, body.TemplateID, body.Enabled, nullIfEmptyJSON(body.Conditions), uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})

	app.Get("/v1/certificates/rules", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := strings.TrimSpace(c.Query("school_id"))
		cid := strings.TrimSpace(c.Query("class_id"))
		type row struct {
			ID, Scope, TemplateID string
			Enabled               bool
			SchoolID              *string `db:"school_id"`
			ClassID               *string `db:"class_id"`
			Conditions            json.RawMessage
		}
		rows := []row{}
		if cid != "" {
			var tutor string
			_ = opts.DB.Get(&tutor, `SELECT tutor_user_id FROM classes WHERE id=$1`, cid)
			allowed := tutor == uid
			if !allowed {
				var sch *string
				_ = opts.DB.Get(&sch, `SELECT school_id FROM classes WHERE id=$1`, cid)
				if sch != nil && *sch != "" {
					allowed, _ = auth.IsSchoolAdmin(opts.DB, *sch, uid)
				}
			}
			if !allowed {
				return fiber.ErrForbidden
			}
			_ = opts.DB.Select(&rows, `SELECT id, scope, template_id, enabled, school_id, class_id, COALESCE(conditions,'{}'::jsonb) AS conditions FROM certificate_rules WHERE class_id=$1 ORDER BY created_at DESC`, cid)
			return c.JSON(fiber.Map{"success": true, "data": rows})
		}
		if sid != "" {
			ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
			if !ok {
				return fiber.ErrForbidden
			}
			_ = opts.DB.Select(&rows, `SELECT id, scope, template_id, enabled, school_id, class_id, COALESCE(conditions,'{}'::jsonb) AS conditions FROM certificate_rules WHERE school_id=$1 ORDER BY created_at DESC`, sid)
			return c.JSON(fiber.Map{"success": true, "data": rows})
		}
		return fiber.ErrBadRequest
	})

	// Background checks endpoints (tutors and school applications)
	app.Post("/v1/tutors/:userId/background-check", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) && !hasSchoolsRole(c, "moderator") {
			return fiber.ErrForbidden
		}
		tid := c.Params("userId")
		var body struct{ Status, Provider, ReportURL string }
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`UPDATE private_tutors SET verification = COALESCE(verification,'{}'::jsonb) || jsonb_build_object('backgroundCheck', jsonb_build_object('status',$1,'provider',$2,'reportUrl',$3)), updated_at=now() WHERE user_id=$4`, body.Status, body.Provider, body.ReportURL, tid)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		logAudit(uid, "tutor_background_check_update", "tutor", tid, body)
		return c.JSON(fiber.Map{"success": true})
	})

	app.Post("/v1/schools/applications/:id/background-check", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) && !hasSchoolsRole(c, "moderator") {
			return fiber.ErrForbidden
		}
		id := c.Params("id")
		var body struct{ Status, Provider, ReportURL string }
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`UPDATE school_applications SET verify = COALESCE(verify,'{}'::jsonb) || jsonb_build_object('backgroundCheck', jsonb_build_object('status',$1,'provider',$2,'reportUrl',$3)), updated_at=now() WHERE id=$4`, body.Status, body.Provider, body.ReportURL, id)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		logAudit(uid, "school_background_check_update", "school_application", id, body)
		return c.JSON(fiber.Map{"success": true})
	})

	app.Patch("/v1/certificates/rules/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var cur struct {
			Scope    string
			SchoolID *string `db:"school_id"`
			ClassID  *string `db:"class_id"`
		}
		if err := opts.DB.Get(&cur, `SELECT scope, school_id, class_id FROM certificate_rules WHERE id=$1`, id); err != nil {
			return fiber.ErrNotFound
		}
		// permission
		if cur.Scope == "school" {
			if cur.SchoolID == nil || *cur.SchoolID == "" {
				return fiber.ErrBadRequest
			}
			ok, _ := auth.IsSchoolAdmin(opts.DB, *cur.SchoolID, uid)
			if !ok {
				return fiber.ErrForbidden
			}
		} else {
			if cur.ClassID == nil || *cur.ClassID == "" {
				return fiber.ErrBadRequest
			}
			var tutor string
			_ = opts.DB.Get(&tutor, `SELECT tutor_user_id FROM classes WHERE id=$1`, *cur.ClassID)
			allowed := tutor == uid
			if !allowed {
				var sch *string
				_ = opts.DB.Get(&sch, `SELECT school_id FROM classes WHERE id=$1`, *cur.ClassID)
				if sch != nil && *sch != "" {
					allowed, _ = auth.IsSchoolAdmin(opts.DB, *sch, uid)
				}
			}
			if !allowed {
				return fiber.ErrForbidden
			}
		}
		var body struct {
			Enabled    *bool           `json:"enabled"`
			Conditions json.RawMessage `json:"conditions"`
		}
		_ = c.BodyParser(&body)
		sets := []string{}
		args := []any{}
		if body.Enabled != nil {
			sets = append(sets, "enabled=$"+strconv.Itoa(len(args)+1))
			args = append(args, *body.Enabled)
		}
		if body.Conditions != nil {
			sets = append(sets, "conditions=$"+strconv.Itoa(len(args)+1))
			args = append(args, nullIfEmptyJSON(body.Conditions))
		}
		if len(sets) == 0 {
			return c.JSON(fiber.Map{"success": true})
		}
		args = append(args, id)
		q := "UPDATE certificate_rules SET " + strings.Join(sets, ", ") + ", updated_at=now() WHERE id=$" + strconv.Itoa(len(args))
		if _, err := opts.DB.Exec(q, args...); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// --- Affiliation Management (Primary + Transfer) ---
	// Get current user's affiliations (optionally by role)
	app.Get("/v1/affiliations", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		role := strings.TrimSpace(strings.ToLower(c.Query("role")))
		type row struct {
			SchoolID  string `json:"schoolId" db:"school_id"`
			Role      string `json:"role" db:"role"`
			Status    string `json:"status" db:"status"`
			IsPrimary bool   `json:"isPrimary" db:"is_primary"`
		}
		rows := []row{}
		var q string
		if role == "" || role == "any" {
			q = `SELECT school_id, role, status, COALESCE(is_primary,false) AS is_primary FROM school_members WHERE user_id=$1 ORDER BY role, is_primary DESC, created_at DESC`
			if err := opts.DB.Select(&rows, q, uid); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		} else {
			q = `SELECT school_id, role, status, COALESCE(is_primary,false) AS is_primary FROM school_members WHERE user_id=$1 AND role=$2 ORDER BY is_primary DESC, created_at DESC`
			if err := opts.DB.Select(&rows, q, uid, role); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	// Set current user's primary affiliation for a role
	app.Post("/v1/affiliations/primary", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			Role     string `json:"role"`
			SchoolID string `json:"schoolId"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		role := strings.ToLower(strings.TrimSpace(body.Role))
		if role == "" {
			return fiber.ErrBadRequest
		}
		sid := strings.TrimSpace(body.SchoolID)
		if sid == "" {
			return fiber.ErrBadRequest
		}
		// Ensure membership exists and is active
		var exists bool
		if err := opts.DB.Get(&exists, `SELECT EXISTS (SELECT 1 FROM school_members WHERE school_id=$1 AND user_id=$2 AND role=$3 AND status='active')`, sid, uid, role); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		if !exists {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "Active membership not found"})
		}
		// Clear others then set primary
		_, _ = opts.DB.Exec(`UPDATE school_members SET is_primary=false WHERE user_id=$1 AND role=$2`, uid, role)
		if _, err := opts.DB.Exec(`UPDATE school_members SET is_primary=true WHERE school_id=$1 AND user_id=$2 AND role=$3`, sid, uid, role); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// Transfer current user's affiliation for a role from one school to another
	app.Post("/v1/affiliations/transfer", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			Role         string `json:"role"`
			FromSchoolID string `json:"fromSchoolId"`
			ToSchoolID   string `json:"toSchoolId"`
			MakePrimary  *bool  `json:"makePrimary"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		role := strings.ToLower(strings.TrimSpace(body.Role))
		fromID := strings.TrimSpace(body.FromSchoolID)
		toID := strings.TrimSpace(body.ToSchoolID)
		if role == "" || fromID == "" || toID == "" || fromID == toID {
			return fiber.ErrBadRequest
		}
		// Verify existing membership
		var ok bool
		if err := opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM school_members WHERE school_id=$1 AND user_id=$2 AND role=$3 AND status='active')`, fromID, uid, role); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		if !ok {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "Source membership not found"})
		}
		// Ensure target membership exists (upsert active)
		if _, err := opts.DB.Exec(`INSERT INTO school_members (school_id, user_id, role, status, is_primary) VALUES ($1,$2,$3,'active',false)
			ON CONFLICT (school_id, user_id, role) DO UPDATE SET status='active'`, toID, uid, role); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		// Optionally deactivate old membership
		_, _ = opts.DB.Exec(`UPDATE school_members SET status='inactive', is_primary=false WHERE school_id=$1 AND user_id=$2 AND role=$3`, fromID, uid, role)
		if body.MakePrimary != nil && *body.MakePrimary {
			_, _ = opts.DB.Exec(`UPDATE school_members SET is_primary=false WHERE user_id=$1 AND role=$2`, uid, role)
			_, _ = opts.DB.Exec(`UPDATE school_members SET is_primary=true WHERE school_id=$1 AND user_id=$2 AND role=$3`, toID, uid, role)
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Post("/v1/schools/:id/members", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		okA, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !okA {
			return fiber.ErrForbidden
		}
		var body struct {
			UserID string `json:"userId"`
			Role   string `json:"role"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		r := strings.TrimSpace(strings.ToLower(body.Role))
		if body.UserID == "" || (r != "admin" && r != "tutor") {
			return fiber.ErrBadRequest
		}
		if _, err := opts.DB.Exec(`INSERT INTO school_members (school_id, user_id, role, status) VALUES ($1,$2,$3,'active')
            ON CONFLICT (school_id, user_id, role) DO UPDATE SET status='active'`, sid, body.UserID, r); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// List pending invites for a school (admin only)
	app.Get("/v1/schools/:id/invites", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		okA, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !okA {
			return fiber.ErrForbidden
		}
		type row struct {
			ID          string     `json:"id" db:"id"`
			Email       *string    `json:"email" db:"email"`
			ExistingUID *string    `json:"existingUserId" db:"existing_user_id"`
			Role        string     `json:"role" db:"role"`
			Status      string     `json:"status" db:"status"`
			InvitedBy   string     `json:"invitedByUserId" db:"invited_by_user_id"`
			Message     *string    `json:"message" db:"message"`
			CreatedAt   time.Time  `json:"createdAt" db:"created_at"`
			RespondedAt *time.Time `json:"respondedAt" db:"responded_at"`
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT id, email, existing_user_id, role, status, invited_by_user_id, message, created_at, responded_at
            FROM school_invites WHERE school_id=$1 ORDER BY created_at DESC`, sid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// --- SMS: Staff (filtered members) ---
	app.Get("/v1/schools/:id/staff", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		member, _ := auth.IsSchoolMember(opts.DB, sid, uid)
		if !member {
			return fiber.ErrForbidden
		}
		var rows []struct{ UserID, Role, Status string }
		if err := opts.DB.Select(&rows, `SELECT user_id, role, status FROM school_members WHERE school_id=$1 AND role IN ('admin','tutor') ORDER BY role, user_id`, sid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// --- SMS: Students (SIS) ---
	app.Get("/v1/schools/:id/students", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		allowed, _ := auth.IsSchoolTutor(opts.DB, sid, uid)
		if !allowed {
			allowed, _ = auth.IsSchoolAdmin(opts.DB, sid, uid)
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var rows []struct{ UserID, AdmissionNo, GradeLevel, Status, DisplayName string }
		q := `SELECT m.user_id AS user_id,
			COALESCE(r.admission_no,'') AS admission_no,
			COALESCE(r.grade_level,'') AS grade_level,
			COALESCE(r.status,'active') AS status,
			COALESCE(up.display_name,'') AS display_name
			FROM school_members m
			LEFT JOIN student_records r ON r.school_id=m.school_id AND r.student_user_id=m.user_id
			LEFT JOIN user_profiles up ON up.user_id=m.user_id
			WHERE m.school_id=$1 AND m.role='student' AND m.status='active' ORDER BY up.display_name NULLS LAST, m.user_id`
		if err := opts.DB.Select(&rows, q, sid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	app.Post("/v1/schools/:id/students", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		allowed, _ := auth.IsSchoolTutor(opts.DB, sid, uid)
		if !allowed {
			allowed, _ = auth.IsSchoolAdmin(opts.DB, sid, uid)
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct{ UserID, AdmissionNo, GradeLevel, Status string }
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.UserID) == "" {
			return fiber.ErrBadRequest
		}
		_, _ = opts.DB.Exec(`INSERT INTO school_members (school_id, user_id, role) VALUES ($1,$2,'student') ON CONFLICT DO NOTHING`, sid, body.UserID)
		_, err = opts.DB.Exec(`INSERT INTO student_records (school_id, student_user_id, admission_no, grade_level, status)
			VALUES ($1,$2, NULLIF($3,''), NULLIF($4,''), COALESCE(NULLIF($5,''),'active'))
			ON CONFLICT (school_id, student_user_id) DO UPDATE SET admission_no=EXCLUDED.admission_no, grade_level=EXCLUDED.grade_level, status=EXCLUDED.status, updated_at=now()`, sid, body.UserID, body.AdmissionNo, body.GradeLevel, body.Status)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true})
	})

	app.Patch("/v1/schools/:id/students/:userId", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		student := c.Params("userId")
		allowed, _ := auth.IsSchoolTutor(opts.DB, sid, uid)
		if !allowed {
			allowed, _ = auth.IsSchoolAdmin(opts.DB, sid, uid)
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct{ AdmissionNo, GradeLevel, Status *string }
		_ = c.BodyParser(&body)
		_, err = opts.DB.Exec(`INSERT INTO student_records (school_id, student_user_id, admission_no, grade_level, status) VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),COALESCE(NULLIF($5,''),'active'))
			ON CONFLICT (school_id, student_user_id) DO UPDATE SET admission_no=COALESCE(NULLIF($3,''), student_records.admission_no), grade_level=COALESCE(NULLIF($4,''), student_records.grade_level), status=COALESCE(NULLIF($5,''), student_records.status), updated_at=now()`,
			sid, student, coalesceString(body.AdmissionNo), coalesceString(body.GradeLevel), coalesceString(body.Status))
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// --- SMS: Academic years and terms ---
	app.Get("/v1/schools/:id/years", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		allowed, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !allowed {
			return fiber.ErrForbidden
		}
		var rows []struct {
			ID, Name, Status, Structure string
			StartDate, EndDate          *time.Time
		}
		if err := opts.DB.Select(&rows, `SELECT id, name, status, structure, start_date, end_date FROM academic_years WHERE school_id=$1 ORDER BY start_date NULLS LAST, created_at DESC`, sid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/schools/:id/years", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		allowed, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct {
			Name               string
			StartDate, EndDate *string
			Status             string
			Structure          string
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Name) == "" {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`INSERT INTO academic_years (school_id, name, start_date, end_date, status, structure) VALUES ($1,$2, NULLIF($3,'')::date, NULLIF($4,'')::date, COALESCE(NULLIF($5,''),'planned'), COALESCE(NULLIF($6,''),'custom'))`, sid, body.Name, coalesceString(body.StartDate), coalesceString(body.EndDate), body.Status, body.Structure)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true})
	})
	app.Patch("/v1/years/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		yid := c.Params("id")
		// verify school admin by joining
		var sid string
		if err := opts.DB.Get(&sid, `SELECT school_id FROM academic_years WHERE id=$1`, yid); err != nil {
			return fiber.ErrNotFound
		}
		allowed, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct{ Name, Status, Structure, StartDate, EndDate *string }
		_ = c.BodyParser(&body)
		_, err = opts.DB.Exec(`UPDATE academic_years SET name=COALESCE(NULLIF($1,''), name), status=COALESCE(NULLIF($2,''), status), structure=COALESCE(NULLIF($3,''), structure), start_date=COALESCE(NULLIF($4,'')::date, start_date), end_date=COALESCE(NULLIF($5,'')::date, end_date) WHERE id=$6`, coalesceString(body.Name), coalesceString(body.Status), coalesceString(body.Structure), coalesceString(body.StartDate), coalesceString(body.EndDate), yid)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// Generate default terms for a year based on structure
	app.Post("/v1/years/:id/generate-terms", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		yid := c.Params("id")
		var row struct {
			SchoolID   string
			Start, End *time.Time
			Structure  string
		}
		if err := opts.DB.Get(&row, `SELECT school_id, start_date AS start, end_date AS end, structure FROM academic_years WHERE id=$1`, yid); err != nil {
			return fiber.ErrNotFound
		}
		allowed, _ := auth.IsSchoolAdmin(opts.DB, row.SchoolID, uid)
		if !allowed {
			return fiber.ErrForbidden
		}
		type bodyT struct{ Structure string }
		var body bodyT
		_ = c.BodyParser(&body)
		structure := strings.ToLower(strings.TrimSpace(body.Structure))
		if structure == "" {
			structure = strings.ToLower(row.Structure)
		}
		if structure == "" {
			structure = "custom"
		}
		var parts int
		switch structure {
		case "semester":
			parts = 2
		case "trimester":
			parts = 3
		case "quarter":
			parts = 4
		default:
			return fiber.ErrBadRequest
		}
		if row.Start == nil || row.End == nil || row.End.Before(*row.Start) {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "year requires valid start and end dates"})
		}
		// clear existing terms
		if _, err := opts.DB.Exec(`DELETE FROM school_terms WHERE year_id=$1`, yid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		dur := row.End.Sub(*row.Start)
		step := time.Duration(int64(dur) / int64(parts+0))
		for i := 0; i < parts; i++ {
			ts := row.Start.Add(step * time.Duration(i))
			te := func() time.Time {
				if i == parts-1 {
					return *row.End
				}
				return row.Start.Add(step*time.Duration(i+1)).AddDate(0, 0, -1)
			}()
			name := fmt.Sprintf("%s %d", strings.Title(structure[:len(structure)-1]), i+1)
			if structure == "quarter" {
				name = fmt.Sprintf("Quarter %d", i+1)
			}
			if structure == "semester" {
				name = fmt.Sprintf("Semester %d", i+1)
			}
			if structure == "trimester" {
				name = fmt.Sprintf("Trimester %d", i+1)
			}
			if _, err := opts.DB.Exec(`INSERT INTO school_terms (year_id, name, start_date, end_date) VALUES ($1,$2,$3,$4)`, yid, name, ts, te); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}
		// update structure field on year
		_, _ = opts.DB.Exec(`UPDATE academic_years SET structure=$1 WHERE id=$2`, structure, yid)
		return c.JSON(fiber.Map{"success": true})
	})
	app.Get("/v1/years/:id/terms", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		yid := c.Params("id")
		var sid string
		if err := opts.DB.Get(&sid, `SELECT school_id FROM academic_years WHERE id=$1`, yid); err != nil {
			return fiber.ErrNotFound
		}
		allowed, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !allowed {
			return fiber.ErrForbidden
		}
		var rows []struct {
			ID, Name           string
			StartDate, EndDate *time.Time
		}
		if err := opts.DB.Select(&rows, `SELECT id, name, start_date, end_date FROM school_terms WHERE year_id=$1 ORDER BY start_date NULLS LAST, created_at DESC`, yid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/years/:id/terms", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		yid := c.Params("id")
		var sid string
		if err := opts.DB.Get(&sid, `SELECT school_id FROM academic_years WHERE id=$1`, yid); err != nil {
			return fiber.ErrNotFound
		}
		allowed, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct {
			Name               string
			StartDate, EndDate *string
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Name) == "" {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`INSERT INTO school_terms (year_id, name, start_date, end_date) VALUES ($1,$2, NULLIF($3,'')::date, NULLIF($4,'')::date)`, yid, body.Name, coalesceString(body.StartDate), coalesceString(body.EndDate))
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true})
	})
	app.Patch("/v1/terms/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		tid := c.Params("id")
		var sid string
		if err := opts.DB.Get(&sid, `SELECT y.school_id FROM school_terms t JOIN academic_years y ON y.id=t.year_id WHERE t.id=$1`, tid); err != nil {
			return fiber.ErrNotFound
		}
		allowed, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct{ Name, StartDate, EndDate *string }
		_ = c.BodyParser(&body)
		_, err = opts.DB.Exec(`UPDATE school_terms SET name=COALESCE(NULLIF($1,''), name), start_date=COALESCE(NULLIF($2,'')::date, start_date), end_date=COALESCE(NULLIF($3,'')::date, end_date) WHERE id=$4`, coalesceString(body.Name), coalesceString(body.StartDate), coalesceString(body.EndDate), tid)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// --- Web Push subscriptions ---
	app.Post("/v1/push/subscribe", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			Endpoint string
			Keys     struct {
				P256dh string `json:"p256dh"`
				Auth   string `json:"auth"`
			}
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Endpoint) == "" {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`INSERT INTO web_push_subscriptions (user_id, endpoint, p256dh, auth) VALUES ($1,$2,$3,$4)
			ON CONFLICT (endpoint) DO UPDATE SET user_id=EXCLUDED.user_id, p256dh=EXCLUDED.p256dh, auth=EXCLUDED.auth, created_at=now()`, uid, body.Endpoint, body.Keys.P256dh, body.Keys.Auth)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Delete("/v1/push/subscribe", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct{ Endpoint string }
		_ = c.BodyParser(&body)
		if strings.TrimSpace(body.Endpoint) == "" {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`DELETE FROM web_push_subscriptions WHERE endpoint=$1 AND user_id=$2`, body.Endpoint, uid)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	app.Get("/v1/push/public-key", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"vapidPublicKey": opts.VAPIDPublicKey}})
	})

	app.Post("/v1/push/test", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var b struct{ Title, Body, Url string }
		_ = c.BodyParser(&b)
		// fetch subs
		type sub struct{ Endpoint, P256dh, Auth string }
		var subs []sub
		if err := opts.DB.Select(&subs, `SELECT endpoint, p256dh, auth FROM web_push_subscriptions WHERE user_id=$1`, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		co := func(s, def string) string {
			if strings.TrimSpace(s) == "" {
				return def
			}
			return s
		}
		payload := map[string]any{"title": co(b.Title, "Notification"), "body": co(b.Body, "Test notification"), "url": co(b.Url, "/")}
		pay, _ := json.Marshal(payload)
		sent := 0
		for _, s := range subs {
			sub := &webpush.Subscription{Endpoint: s.Endpoint, Keys: webpush.Keys{P256dh: s.P256dh, Auth: s.Auth}}
			resp, err := webpush.SendNotification(pay, sub, &webpush.Options{VAPIDPublicKey: opts.VAPIDPublicKey, VAPIDPrivateKey: opts.VAPIDPrivateKey, TTL: 60})
			if err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				sent++
			}
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"sent": sent}})
	})

	// List my push subscriptions
	app.Get("/v1/push/subscriptions", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		type r struct {
			Endpoint, P256dh, Auth string
			CreatedAt              time.Time
		}
		var rs []r
		if err := opts.DB.Select(&rs, `SELECT endpoint, p256dh, auth, created_at FROM web_push_subscriptions WHERE user_id=$1 ORDER BY created_at DESC`, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		type out struct{ Endpoint, P256dh, Auth, CreatedAt string }
		outRows := make([]out, 0, len(rs))
		for _, v := range rs {
			outRows = append(outRows, out{Endpoint: v.Endpoint, P256dh: v.P256dh, Auth: v.Auth, CreatedAt: v.CreatedAt.Format(time.RFC3339)})
		}
		return c.JSON(fiber.Map{"success": true, "data": outRows})
	})

	// --- SMS: Sections ---
	app.Get("/v1/schools/:id/sections", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		member, _ := auth.IsSchoolMember(opts.DB, sid, uid)
		if !member {
			return fiber.ErrForbidden
		}
		var rows []struct{ ID, Name, GradeLevel string }
		if err := opts.DB.Select(&rows, `SELECT id, name, COALESCE(grade_level,'') AS grade_level FROM school_sections WHERE school_id=$1 ORDER BY name`, sid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/schools/:id/sections", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		allowed, _ := auth.IsSchoolTutor(opts.DB, sid, uid)
		if !allowed {
			allowed, _ = auth.IsSchoolAdmin(opts.DB, sid, uid)
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct{ Name, GradeLevel string }
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Name) == "" {
			return fiber.ErrBadRequest
		}
		var out struct{ ID string }
		if err := opts.DB.Get(&out, `INSERT INTO school_sections (school_id, name, grade_level) VALUES ($1,$2, NULLIF($3,'')) RETURNING id`, sid, body.Name, body.GradeLevel); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})
	app.Get("/v1/sections/:id/members", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sec := c.Params("id")
		var sid string
		if err := opts.DB.Get(&sid, `SELECT school_id FROM school_sections WHERE id=$1`, sec); err != nil {
			return fiber.ErrNotFound
		}
		member, _ := auth.IsSchoolMember(opts.DB, sid, uid)
		if !member {
			return fiber.ErrForbidden
		}
		var rows []struct{ StudentUserID string }
		if err := opts.DB.Select(&rows, `SELECT student_user_id FROM section_members WHERE section_id=$1 ORDER BY student_user_id`, sec); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/sections/:id/members", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sec := c.Params("id")
		var sid string
		if err := opts.DB.Get(&sid, `SELECT school_id FROM school_sections WHERE id=$1`, sec); err != nil {
			return fiber.ErrNotFound
		}
		allowed, _ := auth.IsSchoolTutor(opts.DB, sid, uid)
		if !allowed {
			allowed, _ = auth.IsSchoolAdmin(opts.DB, sid, uid)
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct{ StudentUserID string }
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.StudentUserID) == "" {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`INSERT INTO section_members (section_id, student_user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, sec, body.StudentUserID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true})
	})
	app.Delete("/v1/sections/:id/members", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sec := c.Params("id")
		student := strings.TrimSpace(c.Query("student"))
		var sid string
		if err := opts.DB.Get(&sid, `SELECT school_id FROM school_sections WHERE id=$1`, sec); err != nil {
			return fiber.ErrNotFound
		}
		allowed, _ := auth.IsSchoolTutor(opts.DB, sid, uid)
		if !allowed {
			allowed, _ = auth.IsSchoolAdmin(opts.DB, sid, uid)
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		if student == "" {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`DELETE FROM section_members WHERE section_id=$1 AND student_user_id=$2`, sec, student)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// --- SMS: Timetable ---
	app.Get("/v1/schools/:id/timetable", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		member, _ := auth.IsSchoolMember(opts.DB, sid, uid)
		if !member {
			return fiber.ErrForbidden
		}
		q := `SELECT id, class_id, section_id, subject_id, day_of_week, start_time, end_time, COALESCE(room,'') AS room FROM timetable_entries WHERE school_id=$1`
		params := []any{sid}
		if v := strings.TrimSpace(c.Query("sectionId")); v != "" {
			q += " AND section_id=$2"
			params = append(params, v)
		}
		if v := strings.TrimSpace(c.Query("classId")); v != "" {
			if len(params) == 1 {
				q += " AND class_id=$2"
			} else {
				q += " AND class_id=$3"
			}
			params = append(params, v)
		}
		q += " ORDER BY day_of_week, start_time"
		var rows []struct {
			ID, ClassID, SectionID, SubjectID string
			DayOfWeek                         int
			StartTime, EndTime                string
			Room                              string
		}
		if err := opts.DB.Select(&rows, q, params...); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/schools/:id/timetable", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		allowed, _ := auth.IsSchoolTutor(opts.DB, sid, uid)
		if !allowed {
			allowed, _ = auth.IsSchoolAdmin(opts.DB, sid, uid)
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct {
			DayOfWeek                           int
			Start, End                          string
			SubjectID, ClassID, SectionID, Room string
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		if body.DayOfWeek < 0 || body.DayOfWeek > 6 {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`INSERT INTO timetable_entries (school_id, class_id, section_id, subject_id, day_of_week, start_time, end_time, room, created_by_user_id) VALUES ($1, NULLIF($2,''), NULLIF($3,''), NULLIF($4,''), $5, $6::time, $7::time, NULLIF($8,''), $9)`, sid, body.ClassID, body.SectionID, body.SubjectID, body.DayOfWeek, body.Start, body.End, body.Room, uid)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true})
	})
	app.Delete("/v1/timetable/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		tid := c.Params("id")
		var sid string
		if err := opts.DB.Get(&sid, `SELECT school_id FROM timetable_entries WHERE id=$1`, tid); err != nil {
			return fiber.ErrNotFound
		}
		allowed, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !allowed {
			allowed, _ = auth.IsSchoolTutor(opts.DB, sid, uid)
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		_, err = opts.DB.Exec(`DELETE FROM timetable_entries WHERE id=$1`, tid)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// --- SMS: Attendance ---
	app.Post("/v1/classes/:id/attendance/mark", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		cid := c.Params("id")
		allowed, _ := auth.IsClassTutor(opts.DB, cid, uid)
		if !allowed {
			var sid *string
			_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, cid)
			if sid != nil && *sid != "" {
				allowed, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
			}
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct {
			Day     string
			Entries []struct{ StudentUserID, Status string }
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Day) == "" {
			return fiber.ErrBadRequest
		}
		var sid string
		_ = opts.DB.Get(&sid, `SELECT COALESCE(school_id::text,'') FROM classes WHERE id=$1`, cid)
		for _, e := range body.Entries {
			st := strings.ToLower(strings.TrimSpace(e.Status))
			switch st {
			case "present", "absent", "late", "excused":
			default:
				st = "present"
			}
			_, _ = opts.DB.Exec(`INSERT INTO attendance_records (school_id, class_id, day, student_user_id, status, recorded_by_user_id) VALUES ($1,$2,$3::date,$4,$5,$6)
				ON CONFLICT (day, student_user_id, class_id) WHERE class_id IS NOT NULL DO UPDATE SET status=EXCLUDED.status`, sid, cid, body.Day, e.StudentUserID, st, uid)
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Get("/v1/classes/:id/attendance", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		cid := c.Params("id")
		// member check
		if !isClassMember(cid, uid) {
			return fiber.ErrForbidden
		}
		day := strings.TrimSpace(c.Query("day"))
		var rows []struct {
			StudentUserID, Status string
			Day                   time.Time
		}
		if day != "" {
			if err := opts.DB.Select(&rows, `SELECT student_user_id, status, day FROM attendance_records WHERE class_id=$1 AND day=$2::date ORDER BY student_user_id`, cid, day); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		} else {
			if err := opts.DB.Select(&rows, `SELECT student_user_id, status, day FROM attendance_records WHERE class_id=$1 ORDER BY day DESC, student_user_id LIMIT 500`, cid); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	// Create an invite for a school (admin only)
	app.Post("/v1/schools/:id/invites", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		okA, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !okA {
			return fiber.ErrForbidden
		}
		var body struct {
			Email          string  `json:"email"`
			ExistingUserID string  `json:"existingUserId"`
			Role           string  `json:"role"`
			Message        *string `json:"message"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		role := strings.TrimSpace(strings.ToLower(body.Role))
		if role != "admin" && role != "tutor" {
			return fiber.ErrBadRequest
		}
		email := strings.TrimSpace(body.Email)
		existingUID := strings.TrimSpace(body.ExistingUserID)
		if email == "" && existingUID == "" {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "Email or existingUserId is required"})
		}
		token := uuid.NewString()
		var message *string
		if body.Message != nil {
			m := strings.TrimSpace(*body.Message)
			if m != "" {
				message = &m
			}
		}
		var out struct {
			ID          string    `json:"id"`
			Email       *string   `json:"email"`
			ExistingUID *string   `json:"existingUserId"`
			Role        string    `json:"role"`
			Status      string    `json:"status"`
			Token       string    `json:"token"`
			Message     *string   `json:"message"`
			CreatedAt   time.Time `json:"createdAt"`
			InvitedBy   string    `json:"invitedByUserId"`
		}
		if err := opts.DB.Get(&out, `INSERT INTO school_invites (school_id, email, existing_user_id, role, invited_by_user_id, token, message)
				VALUES ($1, NULLIF($2,''), NULLIF($3,''), $4, $5, $6, $7)
				RETURNING id, email, existing_user_id, role, status, token, message, created_at, invited_by_user_id`,
			sid, email, existingUID, role, uid, token, message); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})
	// Detailed school overview for administrators
	app.Get("/v1/schools/:id/overview", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		okA, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !okA {
			return fiber.ErrForbidden
		}
		var school struct {
			ID          string    `json:"id" db:"id"`
			OwnerUserID string    `json:"ownerUserId" db:"owner_user_id"`
			Name        string    `json:"name" db:"name"`
			Description *string   `json:"description,omitempty" db:"description"`
			IsVerified  bool      `json:"isVerified" db:"is_verified"`
			CreatedAt   time.Time `json:"createdAt" db:"created_at"`
			UpdatedAt   time.Time `json:"updatedAt" db:"updated_at"`
		}
		if err := opts.DB.Get(&school, `SELECT id, owner_user_id, name, description, is_verified, created_at, updated_at FROM schools WHERE id=$1`, sid); err != nil {
			return fiber.ErrNotFound
		}
		type classRow struct {
			ID           string    `db:"id"`
			Title        string    `db:"title"`
			Description  *string   `db:"description"`
			TutorUserID  string    `db:"tutor_user_id"`
			TutorName    *string   `db:"tutor_name"`
			Visibility   string    `db:"visibility"`
			IsPaid       bool      `db:"is_paid"`
			PriceCents   int       `db:"price_cents"`
			StudentCount int       `db:"student_count"`
			CreatedAt    time.Time `db:"created_at"`
		}
		classRows := []classRow{}
		if err := opts.DB.Select(&classRows, `
            SELECT c.id, c.title, c.description, c.tutor_user_id,
                   up.display_name AS tutor_name, c.visibility, c.is_paid, c.price_cents,
                   COALESCE(stats.student_count, 0) AS student_count, c.created_at
            FROM classes c
            LEFT JOIN (
                SELECT class_id, SUM(CASE WHEN status='active' THEN 1 ELSE 0 END)::int AS student_count
                FROM class_enrollments
                GROUP BY class_id
            ) stats ON stats.class_id=c.id
            LEFT JOIN user_profiles up ON up.user_id=c.tutor_user_id
            WHERE c.school_id=$1
            ORDER BY c.created_at DESC`, sid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		classIDs := make([]string, 0, len(classRows))
		for _, cr := range classRows {
			classIDs = append(classIDs, cr.ID)
		}
		type studentRow struct {
			ClassID     string  `db:"class_id"`
			UserID      string  `db:"student_user_id"`
			DisplayName *string `db:"display_name"`
		}
		studentRows := []studentRow{}
		if len(classIDs) > 0 {
			query, args, _ := sqlx.In(`SELECT e.class_id, e.student_user_id, up.display_name
                FROM class_enrollments e
                LEFT JOIN user_profiles up ON up.user_id=e.student_user_id
                WHERE e.status='active' AND e.class_id IN (?)
                ORDER BY up.display_name NULLS LAST, e.student_user_id`, classIDs)
			query = opts.DB.Rebind(query)
			if err := opts.DB.Select(&studentRows, query, args...); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}
		type subjectRow struct {
			ID          string  `db:"id"`
			ClassID     string  `db:"class_id"`
			Title       string  `db:"title"`
			Description *string `db:"description"`
			OrderIndex  int     `db:"order_index"`
		}
		subjectRows := []subjectRow{}
		if len(classIDs) > 0 {
			query, args, _ := sqlx.In(`SELECT id, class_id, title, description, order_index
                FROM subjects WHERE class_id IN (?)
                ORDER BY order_index, title`, classIDs)
			query = opts.DB.Rebind(query)
			if err := opts.DB.Select(&subjectRows, query, args...); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}
		subjectIDs := make([]string, 0, len(subjectRows))
		for _, sr := range subjectRows {
			subjectIDs = append(subjectIDs, sr.ID)
		}
		type lessonRow struct {
			ID         string          `db:"id"`
			SubjectID  string          `db:"subject_id"`
			Title      string          `db:"title"`
			Type       string          `db:"type"`
			Content    json.RawMessage `db:"content"`
			OrderIndex int             `db:"order_index"`
			IsFree     bool            `db:"is_free"`
		}
		lessonRows := []lessonRow{}
		if len(subjectIDs) > 0 {
			query, args, _ := sqlx.In(`SELECT id, subject_id, title, type, COALESCE(content,'null'::jsonb) AS content, order_index, is_free
                FROM lessons WHERE subject_id IN (?)
                ORDER BY order_index, title`, subjectIDs)
			query = opts.DB.Rebind(query)
			if err := opts.DB.Select(&lessonRows, query, args...); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}
		lessonIDs := make([]string, 0, len(lessonRows))
		for _, lr := range lessonRows {
			lessonIDs = append(lessonIDs, lr.ID)
		}
		type testRow struct {
			ID          string    `db:"id"`
			SubjectID   *string   `db:"subject_id"`
			LessonID    *string   `db:"lesson_id"`
			Title       string    `db:"title"`
			Description *string   `db:"description"`
			Visibility  string    `db:"visibility"`
			CreatedAt   time.Time `db:"created_at"`
		}
		testRows := []testRow{}
		if len(subjectIDs) > 0 || len(lessonIDs) > 0 {
			var queries []string
			var args []any
			if len(subjectIDs) > 0 {
				q, a, _ := sqlx.In(`SELECT id, subject_id, lesson_id, title, description, visibility, created_at
                    FROM tests WHERE subject_id IN (?)`, subjectIDs)
				queries = append(queries, q)
				args = append(args, a...)
			}
			if len(lessonIDs) > 0 {
				q, a, _ := sqlx.In(`SELECT id, subject_id, lesson_id, title, description, visibility, created_at
                    FROM tests WHERE lesson_id IN (?)`, lessonIDs)
				queries = append(queries, q)
				args = append(args, a...)
			}
			if len(queries) > 0 {
				combined := strings.Join(queries, " UNION ALL ")
				combined = opts.DB.Rebind(combined + " ORDER BY created_at DESC")
				if err := opts.DB.Select(&testRows, combined, args...); err != nil {
					return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
				}
			}
		}
		// Assemble response
		classMap := map[string]fiber.Map{}
		for _, cr := range classRows {
			tutor := fiber.Map{"userId": cr.TutorUserID}
			if cr.TutorName != nil && strings.TrimSpace(*cr.TutorName) != "" {
				tutor["displayName"] = *cr.TutorName
			}
			classMap[cr.ID] = fiber.Map{
				"id":           cr.ID,
				"title":        cr.Title,
				"description":  cr.Description,
				"tutor":        tutor,
				"visibility":   cr.Visibility,
				"isPaid":       cr.IsPaid,
				"priceCents":   cr.PriceCents,
				"studentCount": cr.StudentCount,
				"students":     []fiber.Map{},
				"subjects":     []fiber.Map{},
			}
		}
		for _, sr := range studentRows {
			cls, ok := classMap[sr.ClassID]
			if !ok {
				continue
			}
			students := cls["students"].([]fiber.Map)
			display := strings.TrimSpace(coalesceString(sr.DisplayName))
			if display == "" {
				display = sr.UserID
			}
			students = append(students, fiber.Map{"userId": sr.UserID, "displayName": display})
			cls["students"] = students
		}
		subjectMap := map[string]fiber.Map{}
		for _, sr := range subjectRows {
			subj := fiber.Map{
				"id":          sr.ID,
				"title":       sr.Title,
				"description": sr.Description,
				"orderIndex":  sr.OrderIndex,
				"lessons":     []fiber.Map{},
				"tests":       []fiber.Map{},
			}
			subjectMap[sr.ID] = subj
			if cls, ok := classMap[sr.ClassID]; ok {
				subs := cls["subjects"].([]fiber.Map)
				subs = append(subs, subj)
				cls["subjects"] = subs
			}
		}
		lessonMap := map[string]fiber.Map{}
		for _, lr := range lessonRows {
			lesson := fiber.Map{
				"id":         lr.ID,
				"title":      lr.Title,
				"type":       lr.Type,
				"content":    lr.Content,
				"orderIndex": lr.OrderIndex,
				"isFree":     lr.IsFree,
				"tests":      []fiber.Map{},
			}
			lessonMap[lr.ID] = lesson
			if subj, ok := subjectMap[lr.SubjectID]; ok {
				lessons := subj["lessons"].([]fiber.Map)
				lessons = append(lessons, lesson)
				subj["lessons"] = lessons
			}
		}
		for _, tr := range testRows {
			test := fiber.Map{
				"id":          tr.ID,
				"title":       tr.Title,
				"description": tr.Description,
				"visibility":  tr.Visibility,
				"createdAt":   tr.CreatedAt,
			}
			if tr.LessonID != nil {
				if lesson, ok := lessonMap[*tr.LessonID]; ok {
					tests := lesson["tests"].([]fiber.Map)
					tests = append(tests, test)
					lesson["tests"] = tests
					continue
				}
			}
			if tr.SubjectID != nil {
				if subj, ok := subjectMap[*tr.SubjectID]; ok {
					tests := subj["tests"].([]fiber.Map)
					tests = append(tests, test)
					subj["tests"] = tests
				}
			}
		}
		classes := []fiber.Map{}
		for _, cr := range classRows {
			if cls, ok := classMap[cr.ID]; ok {
				classes = append(classes, cls)
			}
		}
		return c.JSON(fiber.Map{
			"success": true,
			"data": fiber.Map{
				"school":  school,
				"classes": classes,
			},
		})
	})
	// --- Private Tutor Applications ---
	app.Post("/v1/tutors/apply", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body map[string]any
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		// Draft flag allows partial save without strict validation
		draft := false
		if v, ok := body["draft"]; ok {
			switch b := v.(type) {
			case bool:
				draft = b
			case string:
				draft = strings.EqualFold(b, "true") || b == "1"
			}
		}
		// Validate required fields according to onboarding checklist
		gs := func(m map[string]any, keys ...string) string {
			cur := any(m)
			for _, k := range keys {
				mm, ok := cur.(map[string]any)
				if !ok {
					return ""
				}
				cur, ok = mm[k]
				if !ok {
					return ""
				}
			}
			if s, ok := cur.(string); ok {
				return strings.TrimSpace(s)
			}
			return ""
		}
		getb := func(m map[string]any, keys ...string) bool {
			cur := any(m)
			for _, k := range keys {
				mm, ok := cur.(map[string]any)
				if !ok {
					return false
				}
				cur, ok = mm[k]
				if !ok {
					return false
				}
			}
			if b, ok := cur.(bool); ok {
				return b
			}
			return false
		}
		if !draft {
			missing := []string{}
			// Personal Info
			if gs(body, "profile", "legalName") == "" {
				missing = append(missing, "profile.legalName")
			}
			if gs(body, "profile", "displayName") == "" {
				missing = append(missing, "profile.displayName")
			}
			if gs(body, "profile", "email") == "" {
				missing = append(missing, "profile.email")
			}
			if gs(body, "profile", "phone") == "" {
				missing = append(missing, "profile.phone")
			}
			if gs(body, "profile", "country") == "" {
				missing = append(missing, "profile.country")
			}
			if gs(body, "profile", "languages") == "" {
				missing = append(missing, "profile.languages")
			}
			if gs(body, "profile", "timezone") == "" {
				missing = append(missing, "profile.timezone")
			}
			if s, _ := body["bio"].(string); strings.TrimSpace(s) == "" {
				missing = append(missing, "bio")
			}
			// Identity Verification
			if gs(body, "verification", "govIdType") == "" {
				missing = append(missing, "verification.govIdType")
			}
			if gs(body, "verification", "govIdUrl") == "" {
				missing = append(missing, "verification.govIdUrl")
			}
			if gs(body, "verification", "selfieUrl") == "" {
				missing = append(missing, "verification.selfieUrl")
			}
			// Education Proof
			if gs(body, "education", "degreeUrl") == "" {
				missing = append(missing, "education.degreeUrl")
			}
			// Demo content
			if gs(body, "media", "introUrl") == "" {
				missing = append(missing, "media.introUrl")
			}
			if gs(body, "media", "demoUrl") == "" {
				missing = append(missing, "media.demoUrl")
			}
			// Consents
			if !getb(body, "consents", "backgroundCheck") {
				missing = append(missing, "consents.backgroundCheck")
			}
			if !getb(body, "consents", "codeOfConduct") {
				missing = append(missing, "consents.codeOfConduct")
			}
			if !getb(body, "consents", "cleanRecord") {
				missing = append(missing, "consents.cleanRecord")
			}
			if !getb(body, "consents", "lessonRecording") {
				missing = append(missing, "consents.lessonRecording")
			}
			// Payout
			if gs(body, "payout", "method") == "" {
				missing = append(missing, "payout.method")
			}
			if gs(body, "payout", "name") == "" {
				missing = append(missing, "payout.name")
			}
			if len(missing) > 0 {
				return c.Status(400).JSON(fiber.Map{"success": false, "message": "Missing required fields", "missing": missing})
			}
		}
		profile := body["profile"]
		verification := body["verification"]
		education := body["education"]
		teaching := body["teaching"]
		media := body["media"]
		payout := body["payout"]
		consents := body["consents"]
		bio := body["bio"]
		subjects := body["subjects"]
		// optional progress percentage
		var progress *int
		if v, ok := body["progress"].(float64); ok {
			p := int(v)
			if p < 0 {
				p = 0
			}
			if p > 100 {
				p = 100
			}
			progress = &p
		}
		if v, ok := body["progressPct"].(float64); ok {
			p := int(v)
			if p < 0 {
				p = 0
			}
			if p > 100 {
				p = 100
			}
			progress = &p
		}
		desiredStatus := "pending"
		if draft {
			desiredStatus = "draft"
		}
		if _, err := opts.DB.Exec(`INSERT INTO private_tutors (user_id, status, bio, subjects, profile, verification, education, teaching, media, payout, consents, progress_pct)
            VALUES ($1,$12,$2,$3,$4,$5,$6,$7,$8,$9,$10,COALESCE($11,0))
            ON CONFLICT (user_id) DO UPDATE SET status='pending', bio=COALESCE(EXCLUDED.bio, private_tutors.bio), subjects=COALESCE(EXCLUDED.subjects, private_tutors.subjects),
                profile=COALESCE(EXCLUDED.profile, private_tutors.profile), verification=COALESCE(EXCLUDED.verification, private_tutors.verification),
                education=COALESCE(EXCLUDED.education, private_tutors.education), teaching=COALESCE(EXCLUDED.teaching, private_tutors.teaching),
                media=COALESCE(EXCLUDED.media, private_tutors.media), payout=COALESCE(EXCLUDED.payout, private_tutors.payout), consents=COALESCE(EXCLUDED.consents, private_tutors.consents),
                progress_pct=COALESCE(EXCLUDED.progress_pct, private_tutors.progress_pct), updated_at=now()`,
			uid, bio, subjects, profile, verification, education, teaching, media, payout, consents, progress, desiredStatus); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// Submit draft -> pending (validates)
	app.Post("/v1/tutors/submit", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		// Reuse /me to fetch draft and ensure presence
		var row struct {
			Profile      any     `db:"profile"`
			Verification any     `db:"verification"`
			Education    any     `db:"education"`
			Teaching     any     `db:"teaching"`
			Media        any     `db:"media"`
			Payout       any     `db:"payout"`
			Consents     any     `db:"consents"`
			Bio          *string `db:"bio"`
			Subjects     *string `db:"subjects"`
		}
		if err := opts.DB.Get(&row, `SELECT profile, verification, education, teaching, media, payout, consents, bio, subjects FROM private_tutors WHERE user_id=$1`, uid); err != nil {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "No draft found"})
		}
		// Minimal gate: require status change only if currently draft
		var cur string
		_ = opts.DB.Get(&cur, `SELECT status FROM private_tutors WHERE user_id=$1`, uid)
		if cur != "draft" && cur != "rejected" {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "Not in draft/rejected state"})
		}
		// Promote to pending
		if _, err := opts.DB.Exec(`UPDATE private_tutors SET status='pending', updated_at=now() WHERE user_id=$1`, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Get("/v1/tutors/me", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var row struct {
			UserID       string    `json:"userId" db:"user_id"`
			Status       string    `json:"status" db:"status"`
			Bio          *string   `json:"bio,omitempty" db:"bio"`
			Subjects     *string   `json:"subjects,omitempty" db:"subjects"`
			Profile      any       `json:"profile,omitempty" db:"profile"`
			Verification any       `json:"verification,omitempty" db:"verification"`
			Education    any       `json:"education,omitempty" db:"education"`
			Teaching     any       `json:"teaching,omitempty" db:"teaching"`
			Media        any       `json:"media,omitempty" db:"media"`
			Payout       any       `json:"payout,omitempty" db:"payout"`
			Consents     any       `json:"consents,omitempty" db:"consents"`
			Progress     int       `json:"progressPct" db:"progress_pct"`
			CreatedAt    time.Time `json:"createdAt" db:"created_at"`
			UpdatedAt    time.Time `json:"updatedAt" db:"updated_at"`
		}
		err = opts.DB.Get(&row, `SELECT user_id, status, bio, subjects, profile, verification, education, teaching, media, payout, consents, progress_pct, created_at, updated_at FROM private_tutors WHERE user_id=$1`, uid)
		if err != nil {
			return c.JSON(fiber.Map{"success": true, "data": nil})
		}
		return c.JSON(fiber.Map{"success": true, "data": row})
	})
	// --- School Applications ---
	// Create/update application (draft or pending)
	app.Post("/v1/schools/apply", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body map[string]any
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		draft := false
		if v, ok := body["draft"]; ok {
			switch b := v.(type) {
			case bool:
				draft = b
			case string:
				draft = strings.EqualFold(b, "true") || b == "1"
			}
		}
		// helpers
		gs := func(m map[string]any, keys ...string) string {
			cur := any(m)
			for _, k := range keys {
				mm, ok := cur.(map[string]any)
				if !ok {
					return ""
				}
				cur, ok = mm[k]
				if !ok {
					return ""
				}
			}
			if s, ok := cur.(string); ok {
				return strings.TrimSpace(s)
			}
			return ""
		}
		getArrLen := func(m map[string]any, keys ...string) int {
			cur := any(m)
			for _, k := range keys {
				mm, ok := cur.(map[string]any)
				if !ok {
					return 0
				}
				cur, ok = mm[k]
				if !ok {
					return 0
				}
			}
			if a, ok := cur.([]any); ok {
				return len(a)
			}
			return 0
		}
		if !draft {
			missing := []string{}
			if gs(body, "info", "name") == "" {
				missing = append(missing, "info.name")
			}
			if gs(body, "info", "country") == "" {
				missing = append(missing, "info.country")
			}
			if gs(body, "info", "businessType") == "" {
				missing = append(missing, "info.businessType")
			}
			if gs(body, "info", "contact", "name") == "" {
				missing = append(missing, "info.contact.name")
			}
			if gs(body, "info", "contact", "email") == "" {
				missing = append(missing, "info.contact.email")
			}
			if gs(body, "info", "contact", "phone") == "" {
				missing = append(missing, "info.contact.phone")
			}
			if gs(body, "info", "description") == "" {
				missing = append(missing, "info.description")
			}
			if gs(body, "verify", "registrationCertUrl") == "" {
				missing = append(missing, "verify.registrationCertUrl")
			}
			if gs(body, "verify", "taxPinUrl") == "" {
				missing = append(missing, "verify.taxPinUrl")
			}
			if gs(body, "verify", "proofAddressUrl") == "" {
				missing = append(missing, "verify.proofAddressUrl")
			}
			if gs(body, "verify", "founderIdUrl") == "" {
				missing = append(missing, "verify.founderIdUrl")
			}
			if getArrLen(body, "staff", "tutors") <= 0 {
				missing = append(missing, "staff.tutors")
			}
			if gs(body, "finance", "payoutMethod") == "" {
				missing = append(missing, "finance.payoutMethod")
			}
			if gs(body, "finance", "currency") == "" {
				missing = append(missing, "finance.currency")
			}
			if gs(body, "finance", "bankDetails") == "" {
				missing = append(missing, "finance.bankDetails")
			}
			if gs(body, "finance", "revenueModel") == "" {
				missing = append(missing, "finance.revenueModel")
			}
			if gs(body, "curriculum", "subjects") == "" {
				missing = append(missing, "curriculum.subjects")
			}
			if gs(body, "curriculum", "targets") == "" {
				missing = append(missing, "curriculum.targets")
			}
			if gs(body, "curriculum", "format") == "" {
				missing = append(missing, "curriculum.format")
			}
			if gs(body, "curriculum", "languages") == "" {
				missing = append(missing, "curriculum.languages")
			}
			if gs(body, "curriculum", "demoUrl") == "" {
				missing = append(missing, "curriculum.demoUrl")
			}
			// agreements are booleans; check present and true
			needTrue := []string{"partnership", "privacy", "revenueSplit", "codeOfConduct", "quality", "antiFraud", "refund"}
			agr, _ := body["agreements"].(map[string]any)
			for _, k := range needTrue {
				if v, ok := agr[k]; !ok || v != true {
					missing = append(missing, "agreements."+k)
				}
			}
			if len(missing) > 0 {
				return c.Status(400).JSON(fiber.Map{"success": false, "message": "Missing required fields", "missing": missing})
			}
		}
		var progress *int
		if v, ok := body["progress"].(float64); ok {
			p := int(v)
			if p < 0 {
				p = 0
			}
			if p > 100 {
				p = 100
			}
			progress = &p
		}
		if v, ok := body["progressPct"].(float64); ok {
			p := int(v)
			if p < 0 {
				p = 0
			}
			if p > 100 {
				p = 100
			}
			progress = &p
		}
		status := "pending"
		if draft {
			status = "draft"
		}
		info := body["info"]
		verify := body["verify"]
		staff := body["staff"]
		finance := body["finance"]
		curriculum := body["curriculum"]
		agreements := body["agreements"]
		extras := body["extras"]
		if _, err := opts.DB.Exec(`INSERT INTO school_applications (owner_user_id, status, info, verify, staff, finance, curriculum, agreements, extras, progress_pct)
            VALUES ($1,$10,$2,$3,$4,$5,$6,$7,$8,COALESCE($9,0))
            ON CONFLICT (owner_user_id) DO UPDATE SET status='pending', info=COALESCE(EXCLUDED.info, school_applications.info), verify=COALESCE(EXCLUDED.verify, school_applications.verify),
              staff=COALESCE(EXCLUDED.staff, school_applications.staff), finance=COALESCE(EXCLUDED.finance, school_applications.finance), curriculum=COALESCE(EXCLUDED.curriculum, school_applications.curriculum),
              agreements=COALESCE(EXCLUDED.agreements, school_applications.agreements), extras=COALESCE(EXCLUDED.extras, school_applications.extras), progress_pct=COALESCE(EXCLUDED.progress_pct, school_applications.progress_pct), updated_at=now()`,
			uid, info, verify, staff, finance, curriculum, agreements, extras, progress, status); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// Submit draft -> pending
	app.Post("/v1/schools/applications/submit", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var cur string
		_ = opts.DB.Get(&cur, `SELECT status FROM school_applications WHERE owner_user_id=$1`, uid)
		if cur != "draft" && cur != "rejected" {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "Not in draft/rejected state"})
		}
		if _, err := opts.DB.Exec(`UPDATE school_applications SET status='pending', updated_at=now() WHERE owner_user_id=$1`, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// Current user's application
	app.Get("/v1/schools/applications/me", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var row struct {
			ID         string `json:"id" db:"id"`
			Owner      string `json:"ownerUserId" db:"owner_user_id"`
			Status     string `json:"status" db:"status"`
			Info       any    `json:"info" db:"info"`
			Verify     any    `json:"verify" db:"verify"`
			Staff      any    `json:"staff" db:"staff"`
			Finance    any    `json:"finance" db:"finance"`
			Curriculum any    `json:"curriculum" db:"curriculum"`
			Agreements any    `json:"agreements" db:"agreements"`
			Extras     any    `json:"extras" db:"extras"`
			Progress   int    `json:"progressPct" db:"progress_pct"`
		}
		if err := opts.DB.Get(&row, `SELECT id, owner_user_id, status, info, verify, staff, finance, curriculum, agreements, extras, progress_pct FROM school_applications WHERE owner_user_id=$1`, uid); err != nil {
			return c.JSON(fiber.Map{"success": true, "data": nil})
		}
		return c.JSON(fiber.Map{"success": true, "data": row})
	})
	// Platform review of school applications
	app.Get("/v1/schools/applications", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) && !hasSchoolsRole(c, "moderator") {
			return fiber.ErrForbidden
		}
		status := strings.TrimSpace(strings.ToLower(c.Query("status")))
		if status == "" {
			status = "pending"
		}
		type row struct {
			ID          string    `json:"id" db:"id"`
			OwnerUserID string    `json:"ownerUserId" db:"owner_user_id"`
			SchoolName  *string   `json:"schoolName" db:"school_name"`
			SubmittedAt time.Time `json:"submittedAt" db:"updated_at"`
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT id, owner_user_id, info->>'name' AS school_name, updated_at FROM school_applications WHERE status=$1 ORDER BY updated_at DESC LIMIT 200`, status); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/schools/applications/:id/approve", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) && !hasSchoolsRole(c, "moderator") {
			return fiber.ErrForbidden
		}
		id := c.Params("id")
		// Fetch owner and info
		var owner string
		var infoRaw []byte
		if err := opts.DB.Get(&owner, `SELECT owner_user_id FROM school_applications WHERE id=$1`, id); err != nil {
			return fiber.ErrBadRequest
		}
		if err := opts.DB.Get(&infoRaw, `SELECT info FROM school_applications WHERE id=$1`, id); err != nil {
			return fiber.ErrBadRequest
		}
		var info map[string]any
		_ = json.Unmarshal(infoRaw, &info)
		name, _ := info["name"].(string)
		desc, _ := info["description"].(string)
		var descPtr *string
		if strings.TrimSpace(desc) != "" {
			d := desc
			descPtr = &d
		}
		// Create school row if not exists with same owner and name
		var sid string
		err = opts.DB.Get(&sid, `INSERT INTO schools (owner_user_id, name, description, is_verified) VALUES ($1,$2,$3,true) RETURNING id`, owner, name, descPtr)
		if err != nil {
			// If duplicate by unique constraints not present, try to find existing
			_ = opts.DB.Get(&sid, `SELECT id FROM schools WHERE owner_user_id=$1 AND name=$2`, owner, name)
		}
		// Mark application approved
		if _, err := opts.DB.Exec(`UPDATE school_applications SET status='approved', reviewed_by_user_id=$1, reviewed_at=now(), updated_at=now() WHERE id=$2`, uid, id); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		// Enqueue notification for owner
		notif := fiber.Map{"type": "school_application_approved", "applicationId": id, "schoolId": sid}
		nb, _ := json.Marshal(notif)
		_, _ = opts.DB.Exec(`INSERT INTO notifications_queue (recipient_user_id, ntype, payload) VALUES ($1,$2,$3)`, owner, "school_application_approved", nb)
		return c.JSON(fiber.Map{"success": true, "schoolId": sid})
	})
	app.Post("/v1/schools/applications/:id/reject", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) && !hasSchoolsRole(c, "moderator") {
			return fiber.ErrForbidden
		}
		id := c.Params("id")
		var body struct {
			Reason *string `json:"reason"`
		}
		_ = c.BodyParser(&body)
		// write decision note into verify JSON
		_, _ = opts.DB.Exec(`UPDATE school_applications SET verify = COALESCE(verify,'{}'::jsonb) || jsonb_build_object('decision','rejected','reason',COALESCE($1,'')), updated_at=now() WHERE id=$2`, body.Reason, id)
		if _, err := opts.DB.Exec(`UPDATE school_applications SET status='rejected', reviewed_by_user_id=$1, reviewed_at=now(), updated_at=now() WHERE id=$2`, uid, id); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		// Notify owner of rejection
		notif := fiber.Map{"type": "school_application_rejected", "applicationId": id, "reason": body.Reason}
		nb, _ := json.Marshal(notif)
		// Fetch owner id for recipient
		var owner string
		_ = opts.DB.Get(&owner, `SELECT owner_user_id FROM school_applications WHERE id=$1`, id)
		if owner != "" {
			_, _ = opts.DB.Exec(`INSERT INTO notifications_queue (recipient_user_id, ntype, payload) VALUES ($1,$2,$3)`, owner, "school_application_rejected", nb)
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// Platform review of tutor applications (platform admin only)
	app.Get("/v1/tutors/applications", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) && !hasSchoolsRole(c, "moderator") {
			return fiber.ErrForbidden
		}
		status := c.Query("status", "pending")
		type row struct {
			UserID      string    `json:"userId" db:"user_id"`
			Status      string    `json:"status" db:"status"`
			DisplayName *string   `json:"displayName,omitempty" db:"display_name"`
			Email       *string   `json:"email,omitempty" db:"email"`
			SubmittedAt time.Time `json:"submittedAt" db:"updated_at"`
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT pt.user_id, pt.status, up.display_name, up.email, pt.updated_at
            FROM private_tutors pt LEFT JOIN user_profiles up ON up.user_id=pt.user_id WHERE pt.status=$1 ORDER BY pt.updated_at DESC LIMIT 200`, status); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/tutors/:userId/approve", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) && !hasSchoolsRole(c, "moderator") {
			return fiber.ErrForbidden
		}
		tid := c.Params("userId")
		if _, err := opts.DB.Exec(`UPDATE private_tutors SET status='approved', reviewed_by_user_id=$1, reviewed_at=now(), updated_at=now() WHERE user_id=$2`, uid, tid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		// Notify tutor
		nb, _ := json.Marshal(fiber.Map{"type": "tutor_application_approved", "userId": tid})
		_, _ = opts.DB.Exec(`INSERT INTO notifications_queue (recipient_user_id, ntype, payload) VALUES ($1,$2,$3)`, tid, "tutor_application_approved", nb)
		return c.JSON(fiber.Map{"success": true})
	})
	app.Post("/v1/tutors/:userId/reject", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) && !hasSchoolsRole(c, "moderator") {
			return fiber.ErrForbidden
		}
		tid := c.Params("userId")
		var body struct {
			Reason *string `json:"reason"`
		}
		_ = c.BodyParser(&body)
		// store decision in verification json for traceability
		_, _ = opts.DB.Exec(`UPDATE private_tutors SET verification = COALESCE(verification,'{}'::jsonb) || jsonb_build_object('decision','rejected','reason',COALESCE($1,'')), updated_at=now() WHERE user_id=$2`, body.Reason, tid)
		if _, err := opts.DB.Exec(`UPDATE private_tutors SET status='rejected', reviewed_by_user_id=$1, reviewed_at=now(), updated_at=now() WHERE user_id=$2`, uid, tid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		nb, _ := json.Marshal(fiber.Map{"type": "tutor_application_rejected", "userId": tid, "reason": body.Reason})
		_, _ = opts.DB.Exec(`INSERT INTO notifications_queue (recipient_user_id, ntype, payload) VALUES ($1,$2,$3)`, tid, "tutor_application_rejected", nb)
		return c.JSON(fiber.Map{"success": true})
	})
	app.Get("/v1/tutors", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		status := c.Query("status", "approved")
		type row struct {
			UserID   string  `json:"userId" db:"user_id"`
			Status   string  `json:"status" db:"status"`
			Bio      *string `json:"bio,omitempty" db:"bio"`
			Subjects *string `json:"subjects,omitempty" db:"subjects"`
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT user_id, status, bio, subjects FROM private_tutors WHERE status=$1 ORDER BY updated_at DESC`, status); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/progress/lessons", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			LessonID string `json:"lessonId"`
			Status   string `json:"status"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.LessonID) == "" {
			return fiber.ErrBadRequest
		}
		st := body.Status
		if st == "" {
			st = "in_progress"
		}
		if st != "in_progress" && st != "completed" {
			return fiber.ErrBadRequest
		}
		if _, err := opts.DB.Exec(`INSERT INTO lesson_progress (lesson_id, student_user_id, status, last_viewed_at, updated_at)
          VALUES ($1,$2,$3,now(),now())
          ON CONFLICT (lesson_id, student_user_id) DO UPDATE SET status=EXCLUDED.status, last_viewed_at=now(), updated_at=now()`, body.LessonID, uid, st); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// --- Ratings ---
	app.Post("/v1/ratings/tutors", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			TutorUserID string  `json:"tutorUserId"`
			Rating      int     `json:"rating"`
			Comment     *string `json:"comment"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.TutorUserID) == "" || body.Rating < 1 || body.Rating > 5 {
			return fiber.ErrBadRequest
		}
		if _, err := opts.DB.Exec(`INSERT INTO tutor_ratings (tutor_user_id, rater_user_id, rating, comment) VALUES ($1,$2,$3,$4)
            ON CONFLICT (tutor_user_id, rater_user_id) DO UPDATE SET rating=EXCLUDED.rating, comment=EXCLUDED.comment, created_at=now()`, body.TutorUserID, uid, body.Rating, body.Comment); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Get("/v1/ratings/tutors", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		tutorID := c.Query("tutor_user_id")
		if tutorID == "" {
			return fiber.ErrBadRequest
		}
		var avg float64
		var cnt int
		_ = opts.DB.Get(&avg, `SELECT COALESCE(AVG(rating)::float,0) FROM tutor_ratings WHERE tutor_user_id=$1`, tutorID)
		_ = opts.DB.Get(&cnt, `SELECT COUNT(*) FROM tutor_ratings WHERE tutor_user_id=$1`, tutorID)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"tutorUserId": tutorID, "avgRating": avg, "ratingsCount": cnt}})
	})
	app.Get("/v1/ratings/tutors/top", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		limit := 5
		if v := strings.TrimSpace(c.Query("limit")); v != "" {
			if n, err := fmt.Sscanf(v, "%d", &limit); n == 1 && err == nil {
			}
		}
		uid, _ := getUserID(c)
		type row struct {
			TutorUserID string  `json:"tutorUserId" db:"tutor_user_id"`
			Avg         float64 `json:"avgRating" db:"avg_rating"`
			Cnt         int     `json:"ratingsCount" db:"ratings_count"`
		}
		rows := []row{}
		if uid != "" {
			if err := opts.DB.Select(&rows, `SELECT tr.tutor_user_id, AVG(tr.rating)::float AS avg_rating, COUNT(*) AS ratings_count
                FROM tutor_ratings tr
                WHERE NOT EXISTS (SELECT 1 FROM user_blocks b WHERE b.blocker_user_id=$1 AND b.blocked_user_id=tr.tutor_user_id)
                GROUP BY tr.tutor_user_id HAVING COUNT(*)>0
                ORDER BY AVG(tr.rating) DESC, COUNT(*) DESC LIMIT $2`, uid, limit); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			return c.JSON(fiber.Map{"success": true, "data": rows})
		}
		if err := opts.DB.Select(&rows, `SELECT tutor_user_id, AVG(rating)::float AS avg_rating, COUNT(*) AS ratings_count
            FROM tutor_ratings GROUP BY tutor_user_id HAVING COUNT(*)>0 ORDER BY AVG(rating) DESC, COUNT(*) DESC LIMIT $1`, limit); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/ratings/schools", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			SchoolID string  `json:"schoolId"`
			Rating   int     `json:"rating"`
			Comment  *string `json:"comment"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.SchoolID) == "" || body.Rating < 1 || body.Rating > 5 {
			return fiber.ErrBadRequest
		}
		if _, err := opts.DB.Exec(`INSERT INTO school_ratings (school_id, rater_user_id, rating, comment) VALUES ($1,$2,$3,$4)
            ON CONFLICT (school_id, rater_user_id) DO UPDATE SET rating=EXCLUDED.rating, comment=EXCLUDED.comment, created_at=now()`, body.SchoolID, uid, body.Rating, body.Comment); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Get("/v1/ratings/schools", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		schoolID := c.Query("school_id")
		if schoolID == "" {
			return fiber.ErrBadRequest
		}
		var avg float64
		var cnt int
		_ = opts.DB.Get(&avg, `SELECT COALESCE(AVG(rating)::float,0) FROM school_ratings WHERE school_id=$1`, schoolID)
		_ = opts.DB.Get(&cnt, `SELECT COUNT(*) FROM school_ratings WHERE school_id=$1`, schoolID)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"schoolId": schoolID, "avgRating": avg, "ratingsCount": cnt}})
	})
	// --- Public Leaderboards ---
	app.Get("/v1/leaderboards/public", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		limit := 10
		if v := strings.TrimSpace(c.Query("limit")); v != "" {
			if n, err := fmt.Sscanf(v, "%d", &limit); n == 1 && err == nil {
			}
		}
		uid, _ := getUserID(c)
		subjectID := strings.TrimSpace(c.Query("subject_id"))
		type row struct {
			StudentUserID string  `json:"studentUserId" db:"student_user_id"`
			Score         float64 `json:"score" db:"score"`
			TestID        string  `json:"testId" db:"test_id"`
			SubjectID     *string `json:"subjectId,omitempty" db:"subject_id"`
		}
		rows := []row{}
		var err error
		if subjectID != "" {
			if uid != "" {
				err = opts.DB.Select(&rows, `SELECT a.student_user_id, a.score::float AS score, a.test_id, t.subject_id
                    FROM test_attempts a
                    JOIN tests t ON t.id=a.test_id
                    WHERE a.status <> 'in_progress' AND t.visibility='public' AND t.subject_id=$1 AND a.score IS NOT NULL
                      AND NOT EXISTS (SELECT 1 FROM user_blocks b WHERE b.blocker_user_id=$2 AND b.blocked_user_id=a.student_user_id)
                    ORDER BY a.score DESC, a.submitted_at DESC NULLS LAST LIMIT $3`, subjectID, uid, limit)
			} else {
				err = opts.DB.Select(&rows, `SELECT a.student_user_id, a.score::float AS score, a.test_id, t.subject_id
                    FROM test_attempts a
                    JOIN tests t ON t.id=a.test_id
                    WHERE a.status <> 'in_progress' AND t.visibility='public' AND t.subject_id=$1 AND a.score IS NOT NULL
                    ORDER BY a.score DESC, a.submitted_at DESC NULLS LAST LIMIT $2`, subjectID, limit)
			}
		} else {
			if uid != "" {
				err = opts.DB.Select(&rows, `SELECT a.student_user_id, a.score::float AS score, a.test_id, t.subject_id
                    FROM test_attempts a
                    JOIN tests t ON t.id=a.test_id
                    WHERE a.status <> 'in_progress' AND t.visibility='public' AND a.score IS NOT NULL
                      AND NOT EXISTS (SELECT 1 FROM user_blocks b WHERE b.blocker_user_id=$1 AND b.blocked_user_id=a.student_user_id)
                    ORDER BY a.score DESC, a.submitted_at DESC NULLS LAST LIMIT $2`, uid, limit)
			} else {
				err = opts.DB.Select(&rows, `SELECT a.student_user_id, a.score::float AS score, a.test_id, t.subject_id
                    FROM test_attempts a
                    JOIN tests t ON t.id=a.test_id
                    WHERE a.status <> 'in_progress' AND t.visibility='public' AND a.score IS NOT NULL
                    ORDER BY a.score DESC, a.submitted_at DESC NULLS LAST LIMIT $1`, limit)
			}
		}
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	// --- Moderation: Reports and Blocks ---
	app.Post("/v1/moderation/reports", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			TargetType string  `json:"targetType"`
			TargetID   string  `json:"targetId"`
			Reason     *string `json:"reason"`
			Details    *string `json:"details"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.TargetType) == "" || strings.TrimSpace(body.TargetID) == "" {
			return fiber.ErrBadRequest
		}
		// Relationship guard to prevent spam reports
		ttype := strings.ToLower(strings.TrimSpace(body.TargetType))
		allowed := false
		switch ttype {
		case "school":
			var ok bool
			_ = opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM school_members WHERE school_id=$1 AND user_id=$2 AND status='active')`, body.TargetID, uid)
			allowed = ok
		case "class":
			var ok bool
			_ = opts.DB.Get(&ok, `SELECT EXISTS (
                SELECT 1 FROM classes c
                LEFT JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$2
                LEFT JOIN schools s ON s.id=c.school_id
                LEFT JOIN school_members m ON m.school_id=s.id AND m.user_id=$2 AND m.status='active'
                WHERE c.id=$1 AND (c.tutor_user_id=$2 OR e.id IS NOT NULL OR m.id IS NOT NULL)
            )`, body.TargetID, uid)
			allowed = ok
		case "subject":
			var ok bool
			_ = opts.DB.Get(&ok, `SELECT EXISTS (
                SELECT 1 FROM subjects sub
                JOIN classes c ON c.id=sub.class_id
                LEFT JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$2
                LEFT JOIN schools s ON s.id=c.school_id
                LEFT JOIN school_members m ON m.school_id=s.id AND m.user_id=$2 AND m.status='active'
                WHERE sub.id=$1 AND (c.tutor_user_id=$2 OR e.id IS NOT NULL OR m.id IS NOT NULL)
            )`, body.TargetID, uid)
			allowed = ok
		case "lesson":
			var ok bool
			_ = opts.DB.Get(&ok, `SELECT EXISTS (
                SELECT 1 FROM lessons l
                JOIN subjects sub ON sub.id=l.subject_id
                JOIN classes c ON c.id=sub.class_id
                LEFT JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$2
                LEFT JOIN schools s ON s.id=c.school_id
                LEFT JOIN school_members m ON m.school_id=s.id AND m.user_id=$2 AND m.status='active'
                WHERE l.id=$1 AND (c.tutor_user_id=$2 OR e.id IS NOT NULL OR m.id IS NOT NULL)
            )`, body.TargetID, uid)
			allowed = ok
		case "test":
			var ok bool
			_ = opts.DB.Get(&ok, `SELECT EXISTS (
                SELECT 1 FROM tests t
                LEFT JOIN subjects sub ON sub.id=t.subject_id
                LEFT JOIN classes c ON c.id=sub.class_id
                LEFT JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$2
                LEFT JOIN schools s ON s.id=c.school_id
                LEFT JOIN school_members m ON m.school_id=s.id AND m.user_id=$2 AND m.status='active'
                WHERE t.id=$1 AND (t.visibility='public' OR c.tutor_user_id=$2 OR e.id IS NOT NULL OR m.id IS NOT NULL)
            )`, body.TargetID, uid)
			allowed = ok
		case "user":
			var ok bool
			_ = opts.DB.Get(&ok, `SELECT EXISTS (
                SELECT 1 FROM (
                  SELECT c.tutor_user_id AS uid FROM classes c JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$1
                  UNION
                  SELECT e2.student_user_id AS uid FROM class_enrollments e2 WHERE e2.class_id IN (SELECT e.class_id FROM class_enrollments e WHERE e.student_user_id=$1)
                  UNION
                  SELECT m2.user_id AS uid FROM school_members m1 JOIN school_members m2 ON m2.school_id=m1.school_id WHERE m1.user_id=$1 AND m1.status='active' AND m2.status='active'
                ) u WHERE u.uid=$2
            )`, uid, body.TargetID)
			allowed = ok
		default:
			allowed = false
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		if _, err := opts.DB.Exec(`INSERT INTO moderation_reports (reporter_user_id, target_type, target_id, reason, details) VALUES ($1,$2,$3,$4,$5)`, uid, strings.ToLower(body.TargetType), body.TargetID, body.Reason, body.Details); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Get("/v1/moderation/reports", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) {
			return fiber.ErrForbidden
		}
		status := c.Query("status", "open")
		type row struct {
			ID         string    `json:"id" db:"id"`
			Reporter   string    `json:"reporterUserId" db:"reporter_user_id"`
			TargetType string    `json:"targetType" db:"target_type"`
			TargetID   string    `json:"targetId" db:"target_id"`
			Reason     *string   `json:"reason" db:"reason"`
			Details    *string   `json:"details" db:"details"`
			Status     string    `json:"status" db:"status"`
			CreatedAt  time.Time `json:"createdAt" db:"created_at"`
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT id, reporter_user_id, target_type, target_id, reason, details, status, created_at FROM moderation_reports WHERE status=$1 ORDER BY created_at DESC LIMIT 200`, status); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/moderation/reports/:id/resolve", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) {
			return fiber.ErrForbidden
		}
		rid := c.Params("id")
		var body struct {
			Status string `json:"status"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		st := strings.ToLower(strings.TrimSpace(body.Status))
		if st != "reviewed" && st != "dismissed" && st != "action_taken" {
			return fiber.ErrBadRequest
		}
		if _, err := opts.DB.Exec(`UPDATE moderation_reports SET status=$1, reviewed_by_user_id=$2, reviewed_at=now() WHERE id=$3`, st, uid, rid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Get("/v1/users/blocks", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		type row struct {
			BlockedUserID string    `json:"blockedUserId" db:"blocked_user_id"`
			CreatedAt     time.Time `json:"createdAt" db:"created_at"`
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT blocked_user_id, created_at FROM user_blocks WHERE blocker_user_id=$1 ORDER BY created_at DESC`, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/users/blocks", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			BlockedUserID string `json:"blockedUserId"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.BlockedUserID) == "" {
			return fiber.ErrBadRequest
		}
		if _, err := opts.DB.Exec(`INSERT INTO user_blocks (blocker_user_id, blocked_user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, uid, body.BlockedUserID); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Delete("/v1/users/blocks/:blockedUserId", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		bid := c.Params("blockedUserId")
		if _, err := opts.DB.Exec(`DELETE FROM user_blocks WHERE blocker_user_id=$1 AND blocked_user_id=$2`, uid, bid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// --- Search (typeahead) ---
	app.Get("/v1/search", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		t := strings.ToLower(strings.TrimSpace(c.Query("type")))
		q := strings.ToLower(strings.TrimSpace(c.Query("q")))
		if t == "" {
			return fiber.ErrBadRequest
		}
		like := "%" + q + "%"
		switch t {
		case "user":
			// People search with privacy:
			// - Always include verified tutors (private_tutors.status='approved')
			// - Include "related" users only: classmates, class tutors, and members of the same schools
			// - Match against display name, email, uid, and school name(s) tied to the user (via membership or class enrollments)
			type row struct {
				UserID      string  `json:"userId" db:"user_id"`
				DisplayName *string `json:"displayName,omitempty" db:"display_name"`
			}
			rows := []row{}
			scope := strings.ToLower(strings.TrimSpace(c.Query("scope")))
			global := false
			if scope == "global" {
				var count int
				if err := opts.DB.Get(&count, `SELECT COUNT(*) FROM school_members WHERE user_id=$1 AND role='admin' AND status='active'`, uid); err == nil && count > 0 {
					global = true
				}
			}
			if global {
				if err := opts.DB.Select(&rows, `
                    SELECT up.user_id, up.display_name
                    FROM user_profiles up
                    WHERE ($1='' OR up.display_name ILIKE $2 OR COALESCE(up.email,'') ILIKE $2 OR up.user_id ILIKE $2)
                    ORDER BY up.display_name NULLS LAST, up.user_id
                    LIMIT 50`, q, like); err != nil {
					return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
				}
				return c.JSON(fiber.Map{"success": true, "data": rows})
			}
			if err := opts.DB.Select(&rows, `
                WITH related(uid) AS (
                  SELECT c.tutor_user_id FROM classes c JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$1
                  UNION
                  SELECT e2.student_user_id FROM class_enrollments e2 WHERE e2.class_id IN (
                    SELECT e.class_id FROM class_enrollments e WHERE e.student_user_id=$1
                  )
                  UNION
                  SELECT m2.user_id FROM school_members m1
                    JOIN school_members m2 ON m2.school_id=m1.school_id
                  WHERE m1.user_id=$1 AND m1.status='active' AND m2.status='active'
                ),
                approved_tutors(uid) AS (
                  SELECT user_id FROM private_tutors WHERE status='approved'
                ),
                cand(uid) AS (
                  SELECT uid FROM related
                  UNION
                  SELECT uid FROM approved_tutors
                )
                SELECT c.uid AS user_id, up.display_name
                FROM cand c
                LEFT JOIN user_profiles up ON up.user_id=c.uid
                LEFT JOIN school_members sm ON sm.user_id=c.uid
                LEFT JOIN schools sch ON sch.id=sm.school_id
                LEFT JOIN class_enrollments e ON e.student_user_id=c.uid
                LEFT JOIN classes cl ON cl.id=e.class_id
                LEFT JOIN schools sch2 ON sch2.id=cl.school_id
                WHERE ($2='' OR up.display_name ILIKE $3 OR COALESCE(up.email,'') ILIKE $3 OR c.uid ILIKE $3 OR sch.name ILIKE $3 OR sch2.name ILIKE $3)
                GROUP BY c.uid, up.display_name
                LIMIT 20`, uid, q, like); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			return c.JSON(fiber.Map{"success": true, "data": rows})
		case "school":
			type row struct {
				ID   string `json:"id" db:"id"`
				Name string `json:"name" db:"name"`
			}
			rows := []row{}
			if err := opts.DB.Select(&rows, `SELECT s.id, s.name FROM schools s JOIN school_members m ON m.school_id=s.id AND m.user_id=$1 WHERE ($2='' OR s.name ILIKE $3) LIMIT 20`, uid, q, like); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			return c.JSON(fiber.Map{"success": true, "data": rows})
		case "class":
			type row struct {
				ID    string `json:"id" db:"id"`
				Title string `json:"title" db:"title"`
			}
			rows := []row{}
			if err := opts.DB.Select(&rows, `SELECT c.id, c.title FROM classes c
                LEFT JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$1
                LEFT JOIN schools s ON s.id=c.school_id
                LEFT JOIN school_members m ON m.school_id=s.id AND m.user_id=$1 AND m.status='active'
                WHERE (c.tutor_user_id=$1 OR e.id IS NOT NULL OR m.id IS NOT NULL) AND ($2='' OR c.title ILIKE $3)
                ORDER BY c.created_at DESC LIMIT 20`, uid, q, like); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			return c.JSON(fiber.Map{"success": true, "data": rows})
		case "subject":
			type row struct {
				ID    string `json:"id" db:"id"`
				Title string `json:"title" db:"title"`
			}
			rows := []row{}
			if err := opts.DB.Select(&rows, `SELECT sub.id, sub.title FROM subjects sub
                JOIN classes c ON c.id=sub.class_id
                LEFT JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$1
                LEFT JOIN schools s ON s.id=c.school_id
                LEFT JOIN school_members m ON m.school_id=s.id AND m.user_id=$1 AND m.status='active'
                WHERE (c.tutor_user_id=$1 OR e.id IS NOT NULL OR m.id IS NOT NULL) AND ($2='' OR sub.title ILIKE $3)
                ORDER BY sub.order_index, sub.title LIMIT 20`, uid, q, like); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			return c.JSON(fiber.Map{"success": true, "data": rows})
		case "lesson":
			type row struct {
				ID      string `json:"id" db:"id"`
				Title   string `json:"title" db:"title"`
				ClassID string `json:"classId" db:"class_id"`
			}
			rows := []row{}
			if err := opts.DB.Select(&rows, `SELECT l.id, l.title, c.id AS class_id FROM lessons l
                JOIN subjects sub ON sub.id=l.subject_id
                JOIN classes c ON c.id=sub.class_id
                LEFT JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$1
                LEFT JOIN schools s ON s.id=c.school_id
                LEFT JOIN school_members m ON m.school_id=s.id AND m.user_id=$1 AND m.status='active'
                WHERE (c.tutor_user_id=$1 OR e.id IS NOT NULL OR m.id IS NOT NULL) AND ($2='' OR l.title ILIKE $3)
                ORDER BY l.order_index, l.title LIMIT 20`, uid, q, like); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			return c.JSON(fiber.Map{"success": true, "data": rows})
		case "test":
			type row struct {
				ID      string  `json:"id" db:"id"`
				Title   string  `json:"title" db:"title"`
				ClassID *string `json:"classId,omitempty" db:"class_id"`
			}
			rows := []row{}
			if err := opts.DB.Select(&rows, `SELECT t.id, t.title,
                    COALESCE(c.id, c2.id) AS class_id
                FROM tests t
                LEFT JOIN subjects sub ON sub.id=t.subject_id
                LEFT JOIN classes c ON c.id=sub.class_id
                LEFT JOIN lessons l ON l.id=t.lesson_id
                LEFT JOIN subjects sub2 ON sub2.id=l.subject_id
                LEFT JOIN classes c2 ON c2.id=sub2.class_id
                LEFT JOIN class_enrollments e ON e.class_id=COALESCE(c.id, c2.id) AND e.student_user_id=$1
                LEFT JOIN schools s ON s.id=COALESCE(c.school_id, c2.school_id)
                LEFT JOIN school_members m ON m.school_id=s.id AND m.user_id=$1 AND m.status='active'
                WHERE (t.visibility='public' OR COALESCE(c.tutor_user_id, c2.tutor_user_id)=$1 OR e.id IS NOT NULL OR m.id IS NOT NULL)
                  AND ($2='' OR t.title ILIKE $3)
                ORDER BY t.created_at DESC LIMIT 20`, uid, q, like); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			return c.JSON(fiber.Map{"success": true, "data": rows})
		default:
			return fiber.ErrBadRequest
		}
	})

	// --- Direct Messages (1:1) ---
	// Start or get a thread with another user in a class context
	app.Post("/v1/messages/start", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			UserID  string `json:"userId"`
			ClassID string `json:"classId"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.UserID) == "" || strings.TrimSpace(body.ClassID) == "" {
			return fiber.ErrBadRequest
		}
		target := strings.TrimSpace(body.UserID)
		cid := strings.TrimSpace(body.ClassID)
		// Permission: either tutor<->student (enrolled) or guardian<->tutor for that class
		allowed := false
		// tutor <-> student
		isTutor, _ := auth.IsClassTutor(opts.DB, cid, uid)
		var enrolled bool
		_ = opts.DB.Get(&enrolled, `SELECT EXISTS (SELECT 1 FROM class_enrollments WHERE class_id=$1 AND student_user_id=$2 AND status='active')`, cid, target)
		if isTutor && enrolled {
			allowed = true
		}
		// reverse (student to tutor)
		if !allowed {
			isTutorTarget, _ := auth.IsClassTutor(opts.DB, cid, target)
			_ = opts.DB.Get(&enrolled, `SELECT EXISTS (SELECT 1 FROM class_enrollments WHERE class_id=$1 AND student_user_id=$2 AND status='active')`, cid, uid)
			if isTutorTarget && enrolled {
				allowed = true
			}
		}
		// guardian <-> tutor
		if !allowed {
			isTutorTarget, _ := auth.IsClassTutor(opts.DB, cid, target)
			if isTutorTarget {
				var child string
				_ = opts.DB.Get(&child, `SELECT e.student_user_id FROM class_enrollments e WHERE e.class_id=$1 AND EXISTS (SELECT 1 FROM guardians_children g WHERE g.guardian_user_id=$2 AND g.child_user_id=e.student_user_id) LIMIT 1`, cid, uid)
				if strings.TrimSpace(child) != "" {
					allowed = true
				}
			}
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		// find existing thread for pair+class
		var tid string
		err = opts.DB.Get(&tid, `SELECT dt.id FROM direct_threads dt
			JOIN direct_participants p1 ON p1.thread_id=dt.id AND p1.user_id=$1
			JOIN direct_participants p2 ON p2.thread_id=dt.id AND p2.user_id=$2
			WHERE dt.class_id=$3 LIMIT 1`, uid, target, cid)
		if err != nil || tid == "" {
			if err := opts.DB.Get(&tid, `INSERT INTO direct_threads (class_id) VALUES ($1) RETURNING id`, cid); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			_, _ = opts.DB.Exec(`INSERT INTO direct_participants (thread_id, user_id) VALUES ($1,$2),($1,$3) ON CONFLICT DO NOTHING`, tid, uid, target)
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": fiber.Map{"threadId": tid}})
	})

	// List threads for current user
	app.Get("/v1/messages/threads", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		type row struct {
			ID      string     `db:"id"`
			ClassID string     `db:"class_id"`
			Other   string     `db:"other_user"`
			LastMsg *string    `db:"last_msg"`
			LastAt  *time.Time `db:"last_at"`
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT dt.id, dt.class_id,
			(SELECT user_id FROM direct_participants WHERE thread_id=dt.id AND user_id<>$1 LIMIT 1) AS other_user,
			(SELECT m.body FROM direct_messages m WHERE m.thread_id=dt.id ORDER BY m.created_at DESC LIMIT 1) AS last_msg,
			(SELECT m.created_at FROM direct_messages m WHERE m.thread_id=dt.id ORDER BY m.created_at DESC LIMIT 1) AS last_at
			FROM direct_threads dt WHERE EXISTS (SELECT 1 FROM direct_participants p WHERE p.thread_id=dt.id AND p.user_id=$1)
			ORDER BY last_at DESC NULLS LAST, dt.created_at DESC LIMIT 200`, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Get messages for a thread
	app.Get("/v1/messages/threads/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var has bool
		_ = opts.DB.Get(&has, `SELECT EXISTS (SELECT 1 FROM direct_participants WHERE thread_id=$1 AND user_id=$2)`, id, uid)
		if !has {
			return fiber.ErrForbidden
		}
		type msg struct {
			ID, Sender, Body string
			Attachments      json.RawMessage
			CreatedAt        time.Time
		}
		rows := []msg{}
		if err := opts.DB.Select(&rows, `SELECT id, sender_user_id AS sender, COALESCE(body,'') AS body, COALESCE(attachments,'null'::jsonb) AS attachments, created_at FROM direct_messages WHERE thread_id=$1 ORDER BY created_at ASC LIMIT 500`, id); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Send a message
	app.Post("/v1/messages/threads/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var has bool
		_ = opts.DB.Get(&has, `SELECT EXISTS (SELECT 1 FROM direct_participants WHERE thread_id=$1 AND user_id=$2)`, id, uid)
		if !has {
			return fiber.ErrForbidden
		}
		var body struct {
			Body        *string         `json:"body"`
			Attachments json.RawMessage `json:"attachments"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := opts.DB.Get(&out, `INSERT INTO direct_messages (thread_id, sender_user_id, body, attachments) VALUES ($1,$2,$3,$4) RETURNING id`, id, uid, body.Body, nullIfEmptyJSON(body.Attachments)); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		var other string
		_ = opts.DB.Get(&other, `SELECT user_id FROM direct_participants WHERE thread_id=$1 AND user_id<>$2 LIMIT 1`, id, uid)
		if other != "" {
			nb, _ := json.Marshal(fiber.Map{"type": "direct_message", "threadId": id, "messageId": out.ID})
			_, _ = opts.DB.Exec(`INSERT INTO notifications_queue (recipient_user_id, ntype, payload) VALUES ($1,$2,$3)`, other, "direct_message", nb)
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})

	// --- 1:1 Video Calls ---
	roomCode := func() string { return strings.ToUpper(strings.ReplaceAll(uuid.New().String(), "-", ""))[:12] }
	providerBase := func() string {
		b := strings.TrimSpace(os.Getenv("LIVE_PROVIDER_BASE"))
		if b == "" {
			b = "https://meet.jit.si"
		}
		return strings.TrimRight(b, "/")
	}

	// Start a call in a message thread
	app.Post("/v1/messages/threads/:id/call/start", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var has bool
		_ = opts.DB.Get(&has, `SELECT EXISTS (SELECT 1 FROM direct_participants WHERE thread_id=$1 AND user_id=$2)`, id, uid)
		if !has {
			return fiber.ErrForbidden
		}
		// resolve class and other participant
		var classID string
		if err := opts.DB.Get(&classID, `SELECT class_id FROM direct_threads WHERE id=$1`, id); err != nil {
			return fiber.ErrBadRequest
		}
		var other string
		_ = opts.DB.Get(&other, `SELECT user_id FROM direct_participants WHERE thread_id=$1 AND user_id<>$2 LIMIT 1`, id, uid)
		// determine tutor id if any
		var tutorID string
		if ok, _ := auth.IsClassTutor(opts.DB, classID, uid); ok {
			tutorID = uid
		} else if ok, _ := auth.IsClassTutor(opts.DB, classID, other); ok {
			tutorID = other
		}
		code := roomCode()
		url := providerBase() + "/" + code
		// create live room
		_, err = opts.DB.Exec(`INSERT INTO live_rooms (title, purpose, media, interaction, teacher_only, class_id, tutor_user_id, scheduled_at, room_code, provider_url)
			VALUES ($1,'tutor','video','interactive',false,$2, NULLIF($3,''), now(), $4, $5)`, "1:1 Call", classID, tutorID, code, url)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		// notify other
		if other != "" {
			nb, _ := json.Marshal(fiber.Map{"type": "call_invite", "threadId": id, "roomCode": code, "url": url})
			_, _ = opts.DB.Exec(`INSERT INTO notifications_queue (recipient_user_id, ntype, payload) VALUES ($1,$2,$3)`, other, "call_invite", nb)
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"url": url, "roomCode": code}})
	})

	// Get latest active call URL for a thread
	app.Get("/v1/messages/threads/:id/call", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var has bool
		_ = opts.DB.Get(&has, `SELECT EXISTS (SELECT 1 FROM direct_participants WHERE thread_id=$1 AND user_id=$2)`, id, uid)
		if !has {
			return fiber.ErrForbidden
		}
		var classID string
		if err := opts.DB.Get(&classID, `SELECT class_id FROM direct_threads WHERE id=$1`, id); err != nil {
			return fiber.ErrBadRequest
		}
		var url string
		if err := opts.DB.Get(&url, `SELECT provider_url FROM live_rooms WHERE class_id=$1 AND purpose='tutor' AND ended_at IS NULL AND scheduled_at > now()- interval '3 hours' ORDER BY scheduled_at DESC LIMIT 1`, classID); err != nil || strings.TrimSpace(url) == "" {
			return c.JSON(fiber.Map{"success": true, "data": nil})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"url": url}})
	})

	// --- AI Proxy ---
	app.Post("/v1/ai/chat", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		// verify auth
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		// parse a preview to support basic safety and logging
		var body struct {
			Model    string          `json:"model"`
			Context  string          `json:"context"`
			Lang     string          `json:"lang"`
			Messages json.RawMessage `json:"messages"`
		}
		_ = c.BodyParser(&body)
		// enforce AI user settings (opt-out / parental controls)
		var enabled bool = true
		_ = opts.DB.Get(&enabled, `SELECT ai_enabled FROM ai_settings WHERE user_id=$1`, uid)
		if !enabled {
			return c.Status(403).JSON(fiber.Map{"success": false, "message": "AI access is disabled for this account."})
		}
		preview := ""
		if len(body.Messages) > 0 {
			var msgs []map[string]any
			_ = json.Unmarshal(body.Messages, &msgs)
			for i := len(msgs) - 1; i >= 0; i-- { // last user message
				if r, ok := msgs[i]["role"].(string); ok && r == "user" {
					if t, ok := msgs[i]["content"].(string); ok {
						preview = t
						break
					}
				}
			}
		}
		// very light content/academic integrity guard
		bad := []string{"write my assignment", "complete my assignment", "exam answers", "cheat for me"}
		for _, b := range bad {
			if strings.Contains(strings.ToLower(preview), b) {
				// log attempt and block
				_, _ = opts.DB.Exec(`INSERT INTO ai_usage_logs (user_id, model, context, prompt_preview) VALUES ($1,$2,$3,$4)`, uid, body.Model, body.Context, preview)
				return c.Status(400).JSON(fiber.Map{"success": false, "message": "Request blocked by academic integrity safeguards."})
			}
		}
		// record usage
		_, _ = opts.DB.Exec(`INSERT INTO ai_usage_logs (user_id, model, context, prompt_preview) VALUES ($1,$2,$3,$4)`, uid, body.Model, body.Context, preview)
		api := strings.TrimSpace(os.Getenv("AI_API_URL"))
		if api == "" {
			return c.Status(503).JSON(fiber.Map{"success": false, "message": "AI API not configured"})
		}
		req, _ := http.NewRequest("POST", strings.TrimRight(api, "/")+"/v1/chat", bytes.NewReader(c.Body()))
		if v := c.Get("Authorization"); v != "" {
			req.Header.Set("Authorization", v)
		}
		if v := c.Get("Cookie"); v != "" {
			req.Header.Set("Cookie", v)
		}
		if strings.TrimSpace(body.Lang) != "" {
			req.Header.Set("Accept-Language", body.Lang)
		}
		req.Header.Set("Content-Type", "application/json")
		cli := &http.Client{Timeout: 30 * time.Second}
		resp, err := cli.Do(req)
		if err != nil {
			return c.Status(502).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		defer resp.Body.Close()
		c.Set("Content-Type", resp.Header.Get("Content-Type"))
		c.Status(resp.StatusCode)
		_, _ = io.Copy(c, resp.Body)
		return nil
	})

	// --- Feedback & Bug Reports ---
	app.Post("/v1/feedback", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct{ Category, Message, Context string }
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Message) == "" {
			return fiber.ErrBadRequest
		}
		cat := strings.ToLower(strings.TrimSpace(body.Category))
		switch cat {
		case "ux", "feature", "content":
		default:
			cat = "other"
		}
		if _, err := opts.DB.Exec(`INSERT INTO user_feedback (user_id, category, message, context) VALUES ($1,$2,$3,$4)`, uid, cat, body.Message, nullIfEmptyStr(body.Context)); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// --- Error tracking: client error intake ---
	app.Post("/v1/errors", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, _ := getUserID(c) // optional
		var b struct {
			Severity string
			Message  string
			Url      string
			Stack    string
			Context  json.RawMessage
		}
		_ = c.BodyParser(&b)
		sev := strings.ToLower(strings.TrimSpace(b.Severity))
		switch sev {
		case "debug", "info", "warn", "error", "fatal":
		default:
			sev = "error"
		}
		agent := c.Get("User-Agent")
		_, err := opts.DB.Exec(`INSERT INTO error_events (user_id, severity, message, url, stack, context, user_agent) VALUES ($1,$2,$3,$4,$5,$6,$7)`, nullIfEmptyStr(uid), sev, nullIfEmptyStr(b.Message), nullIfEmptyStr(b.Url), nullIfEmptyStr(b.Stack), nullIfEmptyJSON(b.Context), nullIfEmptyStr(agent))
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	app.Get("/v1/feedback/mine", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var rows []struct {
			ID, Category, Message, Context string
			CreatedAt                      time.Time
		}
		if err := opts.DB.Select(&rows, `SELECT id, category, message, COALESCE(context,'') AS context, created_at FROM user_feedback WHERE user_id=$1 ORDER BY created_at DESC LIMIT 200`, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	app.Post("/v1/bugs", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct{ Severity, Title, Details, Url string }
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Title) == "" {
			return fiber.ErrBadRequest
		}
		sev := strings.ToLower(strings.TrimSpace(body.Severity))
		switch sev {
		case "low", "medium", "high", "critical":
		default:
			sev = "low"
		}
		if _, err := opts.DB.Exec(`INSERT INTO bug_reports (user_id, severity, title, details, url) VALUES ($1,$2,$3,$4,$5)`, uid, sev, body.Title, nullIfEmptyStr(body.Details), nullIfEmptyStr(body.Url)); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	app.Get("/v1/bugs/mine", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var rows []struct {
			ID, Severity, Title, Details, Url string
			CreatedAt                         time.Time
		}
		if err := opts.DB.Select(&rows, `SELECT id, severity, title, COALESCE(details,'') AS details, COALESCE(url,'') AS url, created_at FROM bug_reports WHERE user_id=$1 ORDER BY created_at DESC LIMIT 200`, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// --- Parent–Teacher Group Meetings (school-level calls) ---
	app.Post("/v1/schools/:id/meetings", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		// allow school admin or tutor
		isAdmin, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		isTutor, _ := auth.IsSchoolTutor(opts.DB, sid, uid)
		if !isAdmin && !isTutor {
			return fiber.ErrForbidden
		}
		var body struct{ Title string }
		_ = c.BodyParser(&body)
		if strings.TrimSpace(body.Title) == "" {
			body.Title = "Parent–Teacher Meeting"
		}
		code := strings.ToUpper(strings.ReplaceAll(uuid.New().String(), "-", ""))[:12]
		url := strings.TrimRight(providerBase(), "/") + "/" + code
		if _, err := opts.DB.Exec(`INSERT INTO live_rooms (title, purpose, media, interaction, teacher_only, school_id, scheduled_at, room_code, provider_url) VALUES ($1,'study','video','interactive',false,$2, now(), $3, $4)`, body.Title, sid, code, url); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"roomCode": code, "url": url}})
	})

	app.Get("/v1/schools/:id/meetings", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		// verify membership (any role)
		var member bool
		_ = opts.DB.Get(&member, `SELECT EXISTS (SELECT 1 FROM school_members WHERE school_id=$1 AND user_id=$2 AND status='active')`, sid, uid)
		if !member {
			return fiber.ErrForbidden
		}
		var rows []struct {
			ID, Title, RoomCode, ProviderURL string
			ScheduledAt, EndedAt             *time.Time
		}
		if err := opts.DB.Select(&rows, `SELECT id, title, room_code, provider_url, scheduled_at, ended_at FROM live_rooms WHERE school_id=$1 AND scheduled_at > now()- interval '7 days' ORDER BY scheduled_at DESC LIMIT 200`, sid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Stripe webhook to finalize payments
	app.Post("/v1/payments/stripe/webhook", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		secret := strings.TrimSpace(os.Getenv("STRIPE_WEBHOOK_SECRET"))
		if secret == "" {
			return fiber.ErrForbidden
		}
		payload := c.Body()
		sig := c.Get("Stripe-Signature")
		evt, err := webhook.ConstructEvent(payload, sig, secret)
		if err != nil {
			return c.Status(400).SendString("invalid signature")
		}
		switch evt.Type {
		case "checkout.session.completed":
			var s stripe.CheckoutSession
			_ = json.Unmarshal(evt.Data.Raw, &s)
			payID := s.Metadata["payment_id"]
			classID := s.Metadata["class_id"]
			buyer := s.Metadata["buyer_user_id"]
			couponCode := s.Metadata["coupon_code"]
			// subscription checkout flow
			if s.Mode == stripe.CheckoutSessionModeSubscription {
				schoolID := s.Metadata["school_id"]
				planID := s.Metadata["plan_id"]
				if schoolID != "" && planID != "" {
					_, _ = opts.DB.Exec(`INSERT INTO subscriptions (school_id, plan_id, status, stripe_customer_id, stripe_subscription_id, current_period_end)
					VALUES ($1,$2,'active', NULLIF($3,''), NULLIF($4,''), to_timestamp($5))`, schoolID, planID, coalesceString(&s.Customer.ID), coalesceString(&s.Subscription.ID), s.ExpiresAt)
				}
				return c.JSON(fiber.Map{"success": true})
			}
			if payID != "" {
				_, _ = opts.DB.Exec(`UPDATE payments SET status='succeeded', gateway_ref=$1, updated_at=now() WHERE id=$2`, s.ID, payID)
			}
			if classID != "" && buyer != "" {
				_, _ = opts.DB.Exec(`UPDATE class_orders SET status='paid', updated_at=now() WHERE class_id=$1 AND buyer_user_id=$2`, classID, buyer)
				_, _ = opts.DB.Exec(`INSERT INTO class_enrollments (class_id, student_user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, classID, buyer)
				if couponCode != "" {
					var cid string
					_ = opts.DB.Get(&cid, `SELECT id FROM coupons WHERE code=$1`, couponCode)
					if cid != "" {
						_, _ = opts.DB.Exec(`INSERT INTO coupon_redemptions (coupon_id, user_id, class_id, order_id) VALUES ($1,$2,$3,(SELECT id FROM class_orders WHERE payment_id=$4 LIMIT 1))`, cid, buyer, classID, payID)
					}
				}
				// ledger split
				var pct int
				_ = opts.DB.Get(&pct, `SELECT COALESCE((SELECT platform_percent FROM class_commissions WHERE class_id=$1), (SELECT platform_percent FROM commission_settings WHERE id=1))`, classID)
				if pct < 0 {
					pct = 0
				}
				if pct > 100 {
					pct = 100
				}
				var amt int
				_ = opts.DB.Get(&amt, `SELECT price_cents FROM classes WHERE id=$1`, classID)
				platform := (amt * pct) / 100
				tutor := amt - platform
				// partner shares
				type share struct {
					Partner string `db:"partner_user_id"`
					Percent int    `db:"percent"`
				}
				prs := []share{}
				_ = opts.DB.Select(&prs, `SELECT partner_user_id, percent FROM class_revenue_shares WHERE class_id=$1`, classID)
				for _, pr := range prs { // take from tutor share proportionally
					pamt := (amt * pr.Percent) / 100
					if pamt > 0 {
						_, _ = opts.DB.Exec(`INSERT INTO ledger_entries (payment_id, order_id, class_id, user_id, type, amount_cents, currency) VALUES ($1,$2,$3,$4,'partner_revenue',$5,$6)`, payID, nil, classID, pr.Partner, pamt, s.Currency)
						tutor -= pamt
					}
				}
				_, _ = opts.DB.Exec(`INSERT INTO ledger_entries (payment_id, order_id, class_id, user_id, type, amount_cents, currency) VALUES ($1,$2,$3,NULL,'platform_revenue',$4,$5)`, payID, nil, classID, platform, s.Currency)
				var tutorID string
				_ = opts.DB.Get(&tutorID, `SELECT tutor_user_id FROM classes WHERE id=$1`, classID)
				if tutor > 0 && tutorID != "" {
					_, _ = opts.DB.Exec(`INSERT INTO ledger_entries (payment_id, order_id, class_id, user_id, type, amount_cents, currency) VALUES ($1,$2,$3,$4,'tutor_revenue',$5,$6)`, payID, nil, classID, tutorID, tutor, s.Currency)
				}
			}
		case "charge.refunded":
			var ch stripe.Charge
			_ = json.Unmarshal(evt.Data.Raw, &ch)
			ref := ch.ID
			_, _ = opts.DB.Exec(`UPDATE payments SET status='refunded', updated_at=now() WHERE gateway_ref=$1`, ref)
		case "customer.subscription.updated", "customer.subscription.created":
			var sub stripe.Subscription
			_ = json.Unmarshal(evt.Data.Raw, &sub)
			_, _ = opts.DB.Exec(`UPDATE subscriptions SET status=$1, stripe_customer_id=COALESCE(NULLIF($2,''), stripe_customer_id), stripe_subscription_id=COALESCE(NULLIF($3,''), stripe_subscription_id), current_period_end=to_timestamp($4), updated_at=now() WHERE stripe_subscription_id=$3`, string(sub.Status), sub.Customer.ID, sub.ID, sub.CurrentPeriodEnd)
		case "customer.subscription.deleted":
			var sub stripe.Subscription
			_ = json.Unmarshal(evt.Data.Raw, &sub)
			_, _ = opts.DB.Exec(`UPDATE subscriptions SET status='canceled', updated_at=now() WHERE stripe_subscription_id=$1`, sub.ID)
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// Flutterwave webhook
	app.Post("/v1/payments/flutterwave/webhook", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		secret := strings.TrimSpace(os.Getenv("FLW_WEBHOOK_SECRET"))
		if secret != "" {
			hash := c.Get("verif-hash")
			if !strings.EqualFold(strings.TrimSpace(hash), secret) {
				return fiber.ErrForbidden
			}
		}
		var evt struct {
			Data struct {
				TxRef    string `json:"tx_ref"`
				Status   string `json:"status"`
				Id       int64  `json:"id"`
				Currency string `json:"currency"`
			} `json:"data"`
		}
		if err := json.Unmarshal(c.Body(), &evt); err != nil {
			return fiber.ErrBadRequest
		}
		if strings.EqualFold(evt.Data.Status, "successful") && evt.Data.TxRef != "" {
			payID := evt.Data.TxRef
			_, _ = opts.DB.Exec(`UPDATE payments SET status='succeeded', gateway_ref=$1, updated_at=now() WHERE id=$2`, fmt.Sprintf("%d", evt.Data.Id), payID)
			// map order by payment id
			var classID, buyer string
			_ = opts.DB.Get(&classID, `SELECT class_id FROM class_orders WHERE payment_id=$1`, payID)
			_ = opts.DB.Get(&buyer, `SELECT buyer_user_id FROM class_orders WHERE payment_id=$1`, payID)
			if classID != "" && buyer != "" {
				_, _ = opts.DB.Exec(`UPDATE class_orders SET status='paid', updated_at=now() WHERE payment_id=$1`, payID)
				_, _ = opts.DB.Exec(`INSERT INTO class_enrollments (class_id, student_user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, classID, buyer)
				// coupon redemption if present in payments.metadata
				var meta map[string]any
				_ = opts.DB.Get(&meta, `SELECT metadata FROM payments WHERE id=$1`, payID)
				if meta != nil {
					if code, ok := meta["coupon_code"].(string); ok && strings.TrimSpace(code) != "" {
						var cid string
						_ = opts.DB.Get(&cid, `SELECT id FROM coupons WHERE code=$1`, code)
						if cid != "" {
							_, _ = opts.DB.Exec(`INSERT INTO coupon_redemptions (coupon_id, user_id, class_id, order_id) VALUES ($1,$2,$3,(SELECT id FROM class_orders WHERE payment_id=$4 LIMIT 1))`, cid, buyer, classID, payID)
						}
					}
				}
			}
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// M-Pesa callback (STK push)
	app.Post("/v1/payments/mpesa/callback", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		var payload map[string]any
		if err := json.Unmarshal(c.Body(), &payload); err != nil {
			return fiber.ErrBadRequest
		}
		data, _ := payload["Body"].(map[string]any)
		stk, _ := data["stkCallback"].(map[string]any)
		code, _ := stk["ResultCode"].(float64)
		meta, _ := stk["CallbackMetadata"].(map[string]any)
		items, _ := meta["Item"].([]any)
		payID := ""
		receipt := ""
		for _, it := range items {
			kv, _ := it.(map[string]any)
			name, _ := kv["Name"].(string)
			if strings.EqualFold(name, "AccountReference") {
				payID, _ = kv["Value"].(string)
			}
			if strings.EqualFold(name, "MpesaReceiptNumber") {
				receipt, _ = kv["Value"].(string)
			}
		}
		if code == 0 && payID != "" {
			_, _ = opts.DB.Exec(`UPDATE payments SET status='succeeded', gateway_ref=$1, updated_at=now() WHERE id=$2`, receipt, payID)
			var classID, buyer string
			_ = opts.DB.Get(&classID, `SELECT class_id FROM class_orders WHERE payment_id=$1`, payID)
			_ = opts.DB.Get(&buyer, `SELECT buyer_user_id FROM class_orders WHERE payment_id=$1`, payID)
			if classID != "" && buyer != "" {
				_, _ = opts.DB.Exec(`UPDATE class_orders SET status='paid', updated_at=now() WHERE payment_id=$1`, payID)
				_, _ = opts.DB.Exec(`INSERT INTO class_enrollments (class_id, student_user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, classID, buyer)
			}
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// Data export (self or guardian)
	app.Post("/v1/me/export", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		target := c.Query("user_id", uid)
		if target != uid {
			var ok bool
			_ = opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM guardians_children WHERE guardian_user_id=$1 AND child_user_id=$2)`, uid, target)
			if !ok {
				return fiber.ErrForbidden
			}
		}
		var classes []string
		_ = opts.DB.Select(&classes, `SELECT class_id FROM class_enrollments WHERE student_user_id=$1`, target)
		var certs []struct{ ID, Code, Status, Template string }
		_ = opts.DB.Select(&certs, `SELECT id, code, status, template_id AS template FROM certificates WHERE recipient_user_id=$1`, target)
		var attempts int
		_ = opts.DB.Get(&attempts, `SELECT COUNT(*) FROM test_attempts WHERE student_user_id=$1`, target)
		var dm int
		_ = opts.DB.Get(&dm, `SELECT COUNT(*) FROM direct_messages WHERE sender_user_id=$1`, target)
		payload := fiber.Map{"userId": target, "classes": classes, "certificates": certs, "testAttempts": attempts, "messagesSent": dm}
		logAudit(uid, "data_export", "user", target, fiber.Map{"classes": len(classes), "certificates": len(certs)})
		return c.JSON(fiber.Map{"success": true, "data": payload})
	})

	// Data deletion request (Right to be forgotten)
	app.Post("/v1/me/delete", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			Reason string `json:"reason"`
		}
		_ = c.BodyParser(&body)
		_, err = opts.DB.Exec(`INSERT INTO deletion_requests (user_id, reason) VALUES ($1,$2)`, uid, body.Reason)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		logAudit(uid, "deletion_request", "user", uid, body)
		return c.JSON(fiber.Map{"success": true})
	})

	// Basic metrics (JSON)
	start := time.Now()
	app.Get("/v1/metrics", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		var users int
		_ = opts.DB.Get(&users, `SELECT COUNT(DISTINCT user_id) FROM class_enrollments`)
		uptime := time.Since(start).Seconds()
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"uptimeSeconds": uptime, "studentsEnrolled": users}})
	})

	// Billing: plans
	app.Get("/v1/billing/plans", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		var rows []struct {
			ID, Key, Name, Currency, Interval, StripePriceID string
			Price                                            int
			Active                                           bool
		}
		if err := opts.DB.Select(&rows, `SELECT id, pkey AS key, name, price_cents AS price, currency, interval, COALESCE(stripe_price_id,'') AS stripe_price_id, active FROM subscription_plans WHERE active=true ORDER BY price_cents`); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/billing/plans", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) {
			return fiber.ErrForbidden
		}
		var b struct {
			Key, Name                         string
			Price                             int
			Currency, Interval, StripePriceID string
			Active                            *bool
		}
		if err := c.BodyParser(&b); err != nil || strings.TrimSpace(b.Key) == "" || strings.TrimSpace(b.Name) == "" || b.Price <= 0 {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`INSERT INTO subscription_plans (pkey, name, price_cents, currency, interval, stripe_price_id, active) VALUES ($1,$2,$3,COALESCE(NULLIF($4,''),'USD'),COALESCE(NULLIF($5,''),'month'), NULLIF($6,''), COALESCE($7,TRUE))
			ON CONFLICT (pkey) DO UPDATE SET name=EXCLUDED.name, price_cents=EXCLUDED.price_cents, currency=EXCLUDED.currency, interval=EXCLUDED.interval, stripe_price_id=EXCLUDED.stripe_price_id, active=COALESCE($7, subscription_plans.active), updated_at=now()`, b.Key, b.Name, b.Price, b.Currency, b.Interval, b.StripePriceID, b.Active)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// Billing: subscribe school (Stripe hosted session)
	app.Post("/v1/schools/:id/subscribe", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		allowed, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !allowed {
			return fiber.ErrForbidden
		}
		var b struct {
			Plan                  string
			SuccessURL, CancelURL string
		}
		if err := c.BodyParser(&b); err != nil || strings.TrimSpace(b.Plan) == "" {
			return fiber.ErrBadRequest
		}
		var plan struct {
			ID, Stripe         string
			Price              int
			Currency, Interval string
		}
		if err := opts.DB.Get(&plan, `SELECT id, COALESCE(stripe_price_id,'') AS stripe, price_cents AS price, currency, interval FROM subscription_plans WHERE (pkey=$1 OR id::text=$1) AND active=true`, strings.TrimSpace(b.Plan)); err != nil {
			return fiber.ErrNotFound
		}
		if plan.Stripe == "" {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "Plan missing Stripe price id"})
		}
		gateway := strings.TrimSpace(os.Getenv("STRIPE_SECRET_KEY"))
		if gateway == "" {
			return c.Status(503).JSON(fiber.Map{"success": false, "message": "Stripe not configured"})
		}
		stripe.Key = gateway
		params := &stripe.CheckoutSessionParams{
			Mode:       stripe.String(string(stripe.CheckoutSessionModeSubscription)),
			SuccessURL: stripe.String(strings.TrimSpace(b.SuccessURL)),
			CancelURL:  stripe.String(strings.TrimSpace(b.CancelURL)),
			LineItems:  []*stripe.CheckoutSessionLineItemParams{{Price: stripe.String(plan.Stripe), Quantity: stripe.Int64(1)}},
			Metadata:   map[string]string{"school_id": sid, "plan_id": plan.ID},
		}
		s, err := session.New(params)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"url": s.URL}})
	})

	app.Get("/v1/schools/:id/subscription", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		member, _ := auth.IsSchoolMember(opts.DB, sid, uid)
		if !member {
			return fiber.ErrForbidden
		}
		var row struct {
			Status    string
			Plan      string
			PeriodEnd *time.Time
		}
		_ = opts.DB.Get(&row, `SELECT s.status, p.name AS plan, s.current_period_end FROM subscriptions s JOIN subscription_plans p ON p.id=s.plan_id WHERE s.school_id=$1 ORDER BY s.updated_at DESC LIMIT 1`, sid)
		return c.JSON(fiber.Map{"success": true, "data": row})
	})

	// Engagement metrics (simple)
	app.Get("/v1/analytics/engagement", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		_ = uid // any authed user
		var dau int
		_ = opts.DB.Get(&dau, `SELECT COUNT(DISTINCT user_id) FROM (
			SELECT student_user_id AS user_id FROM class_enrollments WHERE created_at > now()- interval '1 day'
			UNION SELECT sender_user_id FROM direct_messages WHERE created_at > now()- interval '1 day'
			UNION SELECT user_id FROM error_events WHERE created_at > now()- interval '1 day'
		) q`)
		var posts int
		_ = opts.DB.Get(&posts, `SELECT COUNT(*) FROM class_posts WHERE created_at > now()- interval '1 day'`)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"dailyActiveUsers": dau, "posts": posts}})
	})

	// Completion metrics (simple class-level aggregation)
	app.Get("/v1/analytics/completion", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		_ = uid
		var avg float64
		_ = opts.DB.Get(&avg, `WITH totals AS (
			SELECT e.class_id, COUNT(DISTINCT l.id) AS lessons
			FROM class_enrollments e JOIN subjects s ON s.class_id=e.class_id JOIN lessons l ON l.subject_id=s.id
			GROUP BY e.class_id
		), done AS (
			SELECT e.class_id, COUNT(*) FILTER (WHERE p.status='completed') AS completed
			FROM class_enrollments e JOIN lesson_progress p ON p.student_user_id=e.student_user_id
			GROUP BY e.class_id
		)
		SELECT COALESCE(AVG(CASE WHEN t.lessons>0 THEN (d.completed::numeric / t.lessons::numeric)*100 ELSE NULL END),0) FROM totals t JOIN done d ON d.class_id=t.class_id`)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"avgLessonCompletionPercent": avg}})
	})

	// Orders and receipts
	app.Get("/v1/orders", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var rows []struct {
			ID        string    `db:"id"`
			Status    string    `db:"status"`
			ClassID   string    `db:"class_id"`
			Title     string    `db:"title"`
			Price     int       `db:"price_cents"`
			Currency  string    `db:"currency"`
			CreatedAt time.Time `db:"created_at"`
		}
		_ = opts.DB.Select(&rows, `SELECT o.id, o.status, o.class_id, c.title, o.price_cents, o.currency, o.created_at FROM class_orders o JOIN classes c ON c.id=o.class_id WHERE o.buyer_user_id=$1 ORDER BY o.created_at DESC`, uid)
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// --- Class versions ---
	app.Get("/v1/classes/:id/versions", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		_ = uid
		cid := c.Params("id")
		var rows []struct {
			ID                 string
			Version            int
			Title, Description string
			CreatedAt          time.Time
		}
		if err := opts.DB.Select(&rows, `SELECT id, version, COALESCE(title,''), COALESCE(description,''), created_at FROM class_versions WHERE class_id=$1 ORDER BY version DESC`, cid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/classes/:id/versions", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		cid := c.Params("id")
		// only tutor can version
		ok, _ := auth.IsClassTutor(opts.DB, cid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var v int
		_ = opts.DB.Get(&v, `SELECT COALESCE(MAX(version),0)+1 FROM class_versions WHERE class_id=$1`, cid)
		var title, desc string
		_ = opts.DB.Get(&title, `SELECT COALESCE(title,'') FROM classes WHERE id=$1`, cid)
		_ = opts.DB.Get(&desc, `SELECT COALESCE(description,'') FROM classes WHERE id=$1`, cid)
		var out struct{ ID string }
		if err := opts.DB.Get(&out, `INSERT INTO class_versions (class_id, version, title, description, created_by_user_id) VALUES ($1,$2,$3,$4,$5) RETURNING id`, cid, v, title, desc, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})

	// --- Peer reviews ---
	app.Get("/v1/classes/:id/peer-reviews", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		cid := c.Params("id")
		if !isClassMember(cid, uid) {
			return fiber.ErrForbidden
		}
		var rows []struct {
			ReviewerUserID string
			Rating         *int
			Comment        string
			CreatedAt      time.Time
		}
		if err := opts.DB.Select(&rows, `SELECT reviewer_user_id, rating, COALESCE(comment,'') AS comment, created_at FROM peer_reviews WHERE class_id=$1 ORDER BY created_at DESC`, cid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		var avg *float64
		_ = opts.DB.Get(&avg, `SELECT AVG(rating)::float FROM peer_reviews WHERE class_id=$1`, cid)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"reviews": rows, "average": avg}})
	})
	app.Post("/v1/classes/:id/peer-reviews", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		cid := c.Params("id")
		if !isClassMember(cid, uid) {
			return fiber.ErrForbidden
		}
		var b struct {
			Rating  int
			Comment string
		}
		if err := c.BodyParser(&b); err != nil || b.Rating < 1 || b.Rating > 5 {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`INSERT INTO peer_reviews (class_id, reviewer_user_id, rating, comment) VALUES ($1,$2,$3, NULLIF($4,'')) ON CONFLICT (class_id, reviewer_user_id) DO UPDATE SET rating=EXCLUDED.rating, comment=EXCLUDED.comment, created_at=now()`, cid, uid, b.Rating, b.Comment)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// --- Experiments bucketing ---
	app.Post("/v1/experiments/assign", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var b struct{ Key string }
		if err := c.BodyParser(&b); err != nil || strings.TrimSpace(b.Key) == "" {
			return fiber.ErrBadRequest
		}
		var existing string
		_ = opts.DB.Get(&existing, `SELECT variant FROM experiment_assignments WHERE exp_key=$1 AND user_id=$2`, b.Key, uid)
		if existing != "" {
			return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"variant": existing}})
		}
		var variants []string
		{
			var raw json.RawMessage
			if err := opts.DB.Get(&raw, `SELECT variants FROM experiments WHERE key=$1`, b.Key); err != nil {
				return fiber.ErrNotFound
			}
			_ = json.Unmarshal(raw, &variants)
		}
		if len(variants) == 0 {
			variants = []string{"A", "B"}
		}
		idx := int(math.Abs(float64(hash(uid+b.Key)))) % len(variants)
		var chosen = variants[idx]
		_, _ = opts.DB.Exec(`INSERT INTO experiment_assignments (exp_key, user_id, variant) VALUES ($1,$2,$3) ON CONFLICT (exp_key, user_id) DO NOTHING`, b.Key, uid, chosen)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"variant": chosen}})
	})

	app.Get("/v1/orders/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var row struct {
			ID, Status, ClassID, Title, Currency string
			Price                                int
			CreatedAt                            time.Time
		}
		if err := opts.DB.Get(&row, `SELECT o.id, o.status, o.class_id, c.title, o.price_cents AS price, o.currency, o.created_at FROM class_orders o JOIN classes c ON c.id=o.class_id WHERE o.id=$1 AND o.buyer_user_id=$2`, id, uid); err != nil {
			return fiber.ErrNotFound
		}
		return c.JSON(fiber.Map{"success": true, "data": row})
	})

	// Refund (admin only) - Stripe and Flutterwave supported
	app.Post("/v1/orders/:id/refund", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) {
			return fiber.ErrForbidden
		}
		id := c.Params("id")
		var payID, gateway, ref string
		_ = opts.DB.Get(&payID, `SELECT payment_id FROM class_orders WHERE id=$1`, id)
		_ = opts.DB.Get(&gateway, `SELECT gateway FROM payments WHERE id=$1`, payID)
		_ = opts.DB.Get(&ref, `SELECT gateway_ref FROM payments WHERE id=$1`, payID)
		if payID == "" || gateway == "" {
			return fiber.ErrBadRequest
		}
		switch strings.ToLower(gateway) {
		case "stripe":
			sec := strings.TrimSpace(os.Getenv("STRIPE_SECRET_KEY"))
			if sec == "" {
				return fiber.ErrBadRequest
			}
			stripe.Key = sec
			_, err = stripeRefund(ref)
			if err != nil {
				return c.Status(502).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		case "flutterwave":
			flw := strings.TrimSpace(os.Getenv("FLW_SECRET_KEY"))
			if flw == "" {
				return fiber.ErrBadRequest
			}
			req, _ := http.NewRequest("POST", "https://api.flutterwave.com/v3/transactions/"+ref+"/refund", bytes.NewReader([]byte(`{}`)))
			req.Header.Set("Authorization", "Bearer "+flw)
			req.Header.Set("Content-Type", "application/json")
			cli := &http.Client{Timeout: 10 * time.Second}
			resp, err2 := cli.Do(req)
			if err2 != nil || resp.StatusCode >= 300 {
				return c.Status(502).JSON(fiber.Map{"success": false, "message": "gateway error"})
			}
			_ = resp.Body.Close()
		default:
			return fiber.ErrBadRequest
		}
		_, _ = opts.DB.Exec(`UPDATE payments SET status='refunded', updated_at=now() WHERE id=$1`, payID)
		_, _ = opts.DB.Exec(`INSERT INTO ledger_entries (payment_id, order_id, class_id, user_id, type, amount_cents, currency) SELECT p.id, o.id, o.class_id, NULL, 'refund', o.price_cents, o.currency FROM payments p JOIN class_orders o ON o.payment_id=p.id WHERE o.id=$1`, id)
		return c.JSON(fiber.Map{"success": true})
	})

	// Payments: checkout with Stripe hosted session when configured
	app.Post("/v1/payments/checkout", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			ClassID    string `json:"classId"`
			Gateway    string `json:"gateway"`
			Currency   string `json:"currency"`
			Mode       string `json:"mode"`
			SuccessURL string `json:"successUrl"`
			CancelURL  string `json:"cancelUrl"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.ClassID) == "" {
			return fiber.ErrBadRequest
		}
		var classTitle string
		var price int
		_ = opts.DB.Get(&classTitle, `SELECT title FROM classes WHERE id=$1`, body.ClassID)
		if err := opts.DB.Get(&price, `SELECT price_cents FROM classes WHERE id=$1 AND is_paid=true`, body.ClassID); err != nil || price <= 0 {
			return fiber.ErrBadRequest
		}
		// Apply coupon if provided and valid
		couponCode := strings.TrimSpace(c.Query("coupon"))
		if couponCode == "" && strings.TrimSpace(body.Mode) != "" { /* noop to use body for reading */
		}
		if couponCode == "" {
			var tmp struct {
				Coupon string `json:"coupon"`
			}
			_ = c.BodyParser(&tmp)
			couponCode = strings.TrimSpace(tmp.Coupon)
		}
		finalPrice := price
		var couponID *string
		if couponCode != "" {
			type cp struct {
				ID       string     `db:"id"`
				Percent  *int       `db:"percent_off"`
				Amount   *int       `db:"amount_off_cents"`
				Currency *string    `db:"currency"`
				Max      *int       `db:"max_redemptions"`
				Expires  *time.Time `db:"expires_at"`
				Active   bool       `db:"active"`
			}
			var cpr cp
			err := opts.DB.Get(&cpr, `SELECT id, percent_off, amount_off_cents, currency, max_redemptions, expires_at, active FROM coupons WHERE code=$1`, couponCode)
			if err == nil && cpr.Active {
				if cpr.Expires == nil || cpr.Expires.After(time.Now()) {
					// enforce max redemptions
					var used int
					_ = opts.DB.Get(&used, `SELECT COUNT(*) FROM coupon_redemptions WHERE coupon_id=$1`, cpr.ID)
					if cpr.Max == nil || used < *cpr.Max {
						discount := 0
						if cpr.Percent != nil {
							discount = (price * (*cpr.Percent)) / 100
						} else if cpr.Amount != nil {
							if cpr.Currency == nil || strings.EqualFold(*cpr.Currency, "USD") { // simple guard; multi-currency later
								discount = *cpr.Amount
							}
						}
						if discount > 0 {
							finalPrice = price - discount
						}
						if finalPrice < 0 {
							finalPrice = 0
						}
						couponID = &cpr.ID
					}
				}
			}
		}
		gateway := body.Gateway
		if gateway == "" {
			gateway = "stripe"
		}
		currency := body.Currency
		if currency == "" {
			currency = "USD"
		}
		var payID string
		// include coupon metadata if present
		var meta any = map[string]any{}
		if couponID != nil || couponCode != "" {
			meta = map[string]any{"coupon_id": couponID, "coupon_code": couponCode}
		}
		mb, _ := json.Marshal(meta)
		if err := opts.DB.Get(&payID, `INSERT INTO payments (user_id, amount_cents, currency, gateway, status, metadata) VALUES ($1,$2,$3,$4,'pending',COALESCE($5,'{}'::jsonb)) RETURNING id`, uid, finalPrice, currency, gateway, nullIfEmptyJSON(mb)); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		_, _ = opts.DB.Exec(`INSERT INTO class_orders (payment_id, class_id, buyer_user_id, price_cents, currency, status) VALUES ($1,$2,$3,$4,$5,'pending') ON CONFLICT (class_id, buyer_user_id) DO NOTHING`, payID, body.ClassID, uid, finalPrice, currency)
		logAudit(uid, "payment_checkout_init", "payment", payID, fiber.Map{"classId": body.ClassID, "amount": finalPrice, "currency": currency, "gateway": gateway, "coupon": couponCode})
		secret := strings.TrimSpace(os.Getenv("STRIPE_SECRET_KEY"))
		if gateway == "stripe" && secret != "" {
			stripe.Key = secret
			params := &stripe.CheckoutSessionParams{
				Mode:       stripe.String(string(stripe.CheckoutSessionModePayment)),
				SuccessURL: stripe.String(strings.TrimSpace(body.SuccessURL)),
				CancelURL:  stripe.String(strings.TrimSpace(body.CancelURL)),
				Metadata:   map[string]string{"payment_id": payID, "class_id": body.ClassID, "buyer_user_id": uid, "coupon_code": couponCode},
				LineItems: []*stripe.CheckoutSessionLineItemParams{
					{
						PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
							Currency: stripe.String(strings.ToLower(currency)),
							ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
								Name: stripe.String(classTitle),
							},
							UnitAmount: stripe.Int64(int64(finalPrice)),
						},
						Quantity: stripe.Int64(1),
					},
				},
			}
			s, err := session.New(params)
			if err == nil && s != nil && s.URL != "" {
				return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"paymentId": payID, "gateway": gateway, "url": s.URL}})
			}
		}
		// Flutterwave hosted checkout
		if gateway == "flutterwave" {
			flw := strings.TrimSpace(os.Getenv("FLW_SECRET_KEY"))
			if flw != "" {
				bodyMap := map[string]any{
					"tx_ref":       payID,
					"amount":       fmt.Sprintf("%.2f", float64(finalPrice)/100.0),
					"currency":     currency,
					"redirect_url": strings.TrimSpace(body.SuccessURL),
					"customer":     map[string]any{"name": uid},
					"meta":         map[string]any{"class_id": body.ClassID, "payment_id": payID, "buyer_user_id": uid, "coupon_code": couponCode},
				}
				pb, _ := json.Marshal(bodyMap)
				req, _ := http.NewRequest("POST", "https://api.flutterwave.com/v3/payments", bytes.NewReader(pb))
				req.Header.Set("Authorization", "Bearer "+flw)
				req.Header.Set("Content-Type", "application/json")
				cli := &http.Client{Timeout: 10 * time.Second}
				resp, err := cli.Do(req)
				if err == nil && resp != nil {
					defer resp.Body.Close()
					var jr map[string]any
					_ = json.NewDecoder(resp.Body).Decode(&jr)
					if d, ok := jr["data"].(map[string]any); ok {
						if link, ok := d["link"].(string); ok && strings.TrimSpace(link) != "" {
							return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"paymentId": payID, "gateway": gateway, "url": link}})
						}
					}
				}
			}
		}
		// M-Pesa STK Push (requires phone in request body)
		if gateway == "mpesa" {
			var phone string
			if v := c.Query("phone"); v != "" {
				phone = v
			}
			type phoneBody struct {
				Phone string `json:"phone"`
			}
			var p phoneBody
			_ = c.BodyParser(&p)
			if strings.TrimSpace(p.Phone) != "" {
				phone = strings.TrimSpace(p.Phone)
			}
			if phone == "" {
				return c.Status(400).JSON(fiber.Map{"success": false, "message": "Phone number required for M-Pesa"})
			}
			ck := strings.TrimSpace(os.Getenv("MPESA_CONSUMER_KEY"))
			cs := strings.TrimSpace(os.Getenv("MPESA_CONSUMER_SECRET"))
			sc := strings.TrimSpace(os.Getenv("MPESA_SHORT_CODE"))
			pk := strings.TrimSpace(os.Getenv("MPESA_PASSKEY"))
			cb := strings.TrimSpace(os.Getenv("MPESA_CALLBACK_URL"))
			base := strings.TrimSpace(os.Getenv("MPESA_BASE_URL"))
			if base == "" {
				base = "https://sandbox.safaricom.co.ke"
			}
			if ck != "" && cs != "" && sc != "" && pk != "" && cb != "" {
				// fetch token
				req, _ := http.NewRequest("GET", base+"/oauth/v1/generate?grant_type=client_credentials", nil)
				req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(ck+":"+cs)))
				cli := &http.Client{Timeout: 10 * time.Second}
				resp, err := cli.Do(req)
				if err == nil && resp != nil {
					var tk struct {
						AccessToken string `json:"access_token"`
					}
					_ = json.NewDecoder(resp.Body).Decode(&tk)
					_ = resp.Body.Close()
					if strings.TrimSpace(tk.AccessToken) != "" {
						ts := time.Now().Format("20060102150405")
						pw := base64.StdEncoding.EncodeToString([]byte(sc + pk + ts))
						stk := map[string]any{
							"BusinessShortCode": sc,
							"Password":          pw,
							"Timestamp":         ts,
							"TransactionType":   "CustomerPayBillOnline",
							"Amount":            finalPrice / 100,
							"PartyA":            phone,
							"PartyB":            sc,
							"PhoneNumber":       phone,
							"CallBackURL":       cb,
							"AccountReference":  payID,
							"TransactionDesc":   classTitle,
						}
						pb, _ := json.Marshal(stk)
						req2, _ := http.NewRequest("POST", base+"/mpesa/stkpush/v1/processrequest", bytes.NewReader(pb))
						req2.Header.Set("Authorization", "Bearer "+tk.AccessToken)
						req2.Header.Set("Content-Type", "application/json")
						resp2, err2 := cli.Do(req2)
						if err2 == nil && resp2 != nil {
							_ = resp2.Body.Close()
							return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"paymentId": payID, "gateway": gateway}})
						}
					}
				}
			}
			return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"paymentId": payID, "gateway": gateway}})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"paymentId": payID, "gateway": gateway}})
	})

	// --- Financial Management: Fee Structures & Invoices ---
	// List fee structures for a school (admin)
	app.Get("/v1/schools/:id/fees", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		type F struct {
			ID, Name, Currency string
			AmountCents        int
			Interval           string
			Active             bool
		}
		rows := []F{}
		_ = opts.DB.Select(&rows, `SELECT id, name, amount_cents, currency, interval, active FROM fee_structures WHERE school_id=$1 ORDER BY created_at DESC`, sid)
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Create/update fee structure
	app.Post("/v1/schools/:id/fees", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			Name        string `json:"name"`
			AmountCents int    `json:"amount_cents"`
			Currency    string `json:"currency"`
			Interval    string `json:"interval"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Name) == "" || body.AmountCents <= 0 {
			return fiber.ErrBadRequest
		}
		if body.Currency == "" {
			body.Currency = "USD"
		}
		if body.Interval == "" {
			body.Interval = "term"
		}
		var id string
		if err := opts.DB.Get(&id, `INSERT INTO fee_structures (school_id,name,amount_cents,currency,interval) VALUES ($1,$2,$3,$4,$5) RETURNING id`, sid, body.Name, body.AmountCents, body.Currency, body.Interval); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": id}})
	})

	// Create an invoice for a student (admin)
	app.Post("/v1/schools/:id/invoices", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			StudentUserID string  `json:"student_user_id"`
			Currency      string  `json:"currency"`
			DueDate       *string `json:"due_date"`
			Items         []struct {
				Description string `json:"description"`
				AmountCents int    `json:"amount_cents"`
				Quantity    int    `json:"quantity"`
			} `json:"items"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.StudentUserID) == "" || len(body.Items) == 0 {
			return fiber.ErrBadRequest
		}
		if body.Currency == "" {
			body.Currency = "USD"
		}
		total := 0
		for _, it := range body.Items {
			if it.AmountCents <= 0 || strings.TrimSpace(it.Description) == "" {
				return fiber.ErrBadRequest
			}
			q := it.Quantity
			if q <= 0 {
				q = 1
			}
			total += it.AmountCents * q
		}
		// Apply scholarship if active
		var disc int
		var sch struct {
			Percent *int    `db:"percent_off"`
			Amount  *int    `db:"amount_off_cents"`
			Curr    *string `db:"currency"`
		}
		err = opts.DB.Get(&sch, `SELECT percent_off, amount_off_cents, currency FROM scholarships WHERE school_id=$1 AND student_user_id=$2 AND (valid_from IS NULL OR valid_from<=CURRENT_DATE) AND (valid_to IS NULL OR valid_to>=CURRENT_DATE) ORDER BY created_at DESC LIMIT 1`, sid, body.StudentUserID)
		if err == nil {
			if sch.Percent != nil {
				disc = (total * (*sch.Percent)) / 100
			}
			if sch.Amount != nil {
				if sch.Curr == nil || strings.EqualFold(*sch.Curr, body.Currency) {
					disc += *sch.Amount
				}
			}
			if disc > total {
				disc = total
			}
		}
		grand := total - disc
		var invID string
		if err := opts.DB.Get(&invID, `INSERT INTO invoices (school_id, student_user_id, status, total_cents, currency, due_date) VALUES ($1,$2,'open',$3,$4,COALESCE($5::date,NULL)) RETURNING id`, sid, body.StudentUserID, grand, body.Currency, body.DueDate); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		for _, it := range body.Items {
			q := it.Quantity
			if q <= 0 {
				q = 1
			}
			_, _ = opts.DB.Exec(`INSERT INTO invoice_items (invoice_id, description, amount_cents, quantity) VALUES ($1,$2,$3,$4)`, invID, it.Description, it.AmountCents, q)
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": invID, "total_cents": grand, "currency": body.Currency}})
	})

	// Create scholarship/discount for a student (admin)
	app.Post("/v1/schools/:id/scholarships", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			StudentUserID  string  `json:"student_user_id"`
			PercentOff     *int    `json:"percent_off"`
			AmountOffCents *int    `json:"amount_off_cents"`
			Currency       *string `json:"currency"`
			ValidFrom      *string `json:"valid_from"`
			ValidTo        *string `json:"valid_to"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.StudentUserID) == "" {
			return fiber.ErrBadRequest
		}
		if body.PercentOff == nil && body.AmountOffCents == nil {
			return fiber.ErrBadRequest
		}
		var id string
		if err := opts.DB.Get(&id, `INSERT INTO scholarships (school_id, student_user_id, percent_off, amount_off_cents, currency, valid_from, valid_to) VALUES ($1,$2,$3,$4,$5,COALESCE($6::date,NULL),COALESCE($7::date,NULL)) RETURNING id`, sid, body.StudentUserID, body.PercentOff, body.AmountOffCents, body.Currency, body.ValidFrom, body.ValidTo); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": id}})
	})

	// List my invoices (student/parent)
	app.Get("/v1/me/invoices", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		rows := []struct {
			ID       string  `db:"id" json:"id"`
			SchoolID string  `db:"school_id" json:"school_id"`
			Status   string  `db:"status" json:"status"`
			Total    int     `db:"total_cents" json:"total_cents"`
			Currency string  `db:"currency" json:"currency"`
			Due      *string `db:"due_date" json:"due_date"`
		}{}
		_ = opts.DB.Select(&rows, `SELECT id, school_id, status, total_cents, currency, to_char(due_date,'YYYY-MM-DD') AS due_date FROM invoices WHERE student_user_id=$1 ORDER BY created_at DESC`, uid)
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Return Core payment intent parameters for an invoice
	app.Get("/v1/invoices/:id/core-payment", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var row struct {
			Student  string `db:"student_user_id"`
			Total    int    `db:"total_cents"`
			Currency string `db:"currency"`
		}
		if err := opts.DB.Get(&row, `SELECT student_user_id, total_cents, currency FROM invoices WHERE id=$1`, id); err != nil {
			return fiber.ErrNotFound
		}
		if row.Student != uid && !isPlatformAdmin(uid) {
			return fiber.ErrForbidden
		}
		desc := fmt.Sprintf("schools:invoice:%s", id)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"amount_cents": row.Total, "currency": row.Currency, "description": desc}})
	})

	// Record invoice payment result from Core billing
	app.Post("/v1/invoices/:id/payments", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var body struct {
			CoreIntentID *int64 `json:"core_intent_id"`
			Status       string `json:"status"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Status) == "" {
			return fiber.ErrBadRequest
		}
		var student string
		_ = opts.DB.Get(&student, `SELECT student_user_id FROM invoices WHERE id=$1`, id)
		if student == "" {
			return fiber.ErrNotFound
		}
		if student != uid && !isPlatformAdmin(uid) {
			return fiber.ErrForbidden
		}
		st := strings.ToLower(strings.TrimSpace(body.Status))
		if st == "succeeded" || st == "paid" {
			_, _ = opts.DB.Exec(`UPDATE invoices SET status='paid', core_intent_id=COALESCE($1, core_intent_id), updated_at=now() WHERE id=$2`, body.CoreIntentID, id)
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// Financial reporting summary per school
	app.Get("/v1/schools/:id/reports/finance", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		from := strings.TrimSpace(c.Query("from"))
		to := strings.TrimSpace(c.Query("to"))
		cond := "WHERE school_id=$1"
		args := []any{sid}
		if from != "" {
			cond += " AND created_at::date >= $2"
			args = append(args, from)
		}
		if to != "" {
			if len(args) == 1 {
				cond += " AND created_at::date <= $2"
			} else {
				cond += " AND created_at::date <= $3"
			}
			args = append(args, to)
		}
		type Row struct {
			Currency    string `db:"currency" json:"currency"`
			Invoiced    int    `db:"invoiced" json:"invoiced_cents"`
			Paid        int    `db:"paid" json:"paid_cents"`
			Outstanding int    `db:"outstanding" json:"outstanding_cents"`
		}
		rows := []Row{}
		q := "SELECT currency, COALESCE(SUM(total_cents),0) FILTER (WHERE status IN ('open','paid')) AS invoiced, COALESCE(SUM(total_cents),0) FILTER (WHERE status='paid') AS paid, COALESCE(SUM(total_cents),0) FILTER (WHERE status='open') AS outstanding FROM invoices " + cond + " GROUP BY currency"
		if err := opts.DB.Select(&rows, opts.DB.Rebind(q), args...); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// --- School-wide Announcements ---
	// Create announcement (admin or tutor)
	app.Post("/v1/schools/:id/announcements", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		isAdmin, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		isTutor, _ := auth.IsSchoolTutor(opts.DB, sid, uid)
		if !isAdmin && !isTutor {
			return fiber.ErrForbidden
		}
		var body struct {
			Title      string `json:"title"`
			Body       string `json:"body"`
			Visibility string `json:"visibility"`
			Pinned     *bool  `json:"pinned"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Title) == "" || strings.TrimSpace(body.Body) == "" {
			return fiber.ErrBadRequest
		}
		vis := strings.ToLower(strings.TrimSpace(body.Visibility))
		if vis == "" {
			vis = "school"
		}
		var id string
		if err := opts.DB.Get(&id, `INSERT INTO announcements (school_id, author_user_id, title, body, visibility, pinned) VALUES ($1,$2,$3,$4,$5,COALESCE($6,false)) RETURNING id`, sid, uid, body.Title, body.Body, vis, body.Pinned); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": id}})
	})

	// --- School Events ---
	// Create event (admin or tutor)
	app.Post("/v1/schools/:id/events", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		isAdmin, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		isTutor, _ := auth.IsSchoolTutor(opts.DB, sid, uid)
		if !isAdmin && !isTutor {
			return fiber.ErrForbidden
		}
		var body struct {
			Title       string  `json:"title"`
			Description *string `json:"description"`
			Location    *string `json:"location"`
			StartsAt    string  `json:"starts_at"`
			EndsAt      *string `json:"ends_at"`
			Visibility  string  `json:"visibility"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Title) == "" || strings.TrimSpace(body.StartsAt) == "" {
			return fiber.ErrBadRequest
		}
		vis := strings.ToLower(strings.TrimSpace(body.Visibility))
		if vis == "" {
			vis = "school"
		}
		var id string
		if err := opts.DB.Get(&id, `INSERT INTO events (school_id, created_by_user_id, title, description, location, starts_at, ends_at, visibility) VALUES ($1,$2,$3,$4,$5,$6,COALESCE($7::timestamptz,NULL),$8) RETURNING id`, sid, uid, body.Title, body.Description, body.Location, body.StartsAt, body.EndsAt, vis); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": id}})
	})

	// List school events
	app.Get("/v1/schools/:id/events", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uuid, _ := getUserID(c)
		sid := c.Params("id")
		isMember, _ := auth.IsSchoolMember(opts.DB, sid, uuid)
		from := strings.TrimSpace(c.Query("from"))
		to := strings.TrimSpace(c.Query("to"))
		limit := 100
		if v := c.Query("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
				limit = n
			}
		}
		offset := 0
		if v := c.Query("offset"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				offset = n
			}
		}
		where := []string{"school_id=$1"}
		args := []any{sid}
		if !isMember {
			where = append(where, "visibility='public'")
		}
		if from != "" {
			where = append(where, "starts_at::date >= $2")
			args = append(args, from)
		}
		if to != "" {
			where = append(where, "starts_at::date <= $3")
			args = append(args, to)
		}
		cond := strings.Join(where, " AND ")
		type E struct {
			ID, Title, Description, Location, Visibility, CreatedBy string
			StartsAt                                                *time.Time
			EndsAt                                                  *time.Time
		}
		rows := []E{}
		q := "SELECT id, title, COALESCE(description,'') AS description, COALESCE(location,'') AS location, visibility, created_by_user_id AS created_by, starts_at, ends_at FROM events WHERE " + cond + " ORDER BY starts_at ASC LIMIT $4 OFFSET $5"
		args = append(args, limit, offset)
		if err := opts.DB.Select(&rows, opts.DB.Rebind(q), args...); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Event details (respect visibility)
	app.Get("/v1/events/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uuid, _ := getUserID(c)
		id := c.Params("id")
		var row struct {
			ID, SchoolID, Title, Description, Location, Visibility, CreatedBy string
			StartsAt                                                          time.Time
			EndsAt                                                            *time.Time
		}
		if err := opts.DB.Get(&row, `SELECT id, school_id, title, COALESCE(description,'') AS description, COALESCE(location,'') AS location, visibility, created_by_user_id AS created_by, starts_at, ends_at FROM events WHERE id=$1`, id); err != nil {
			return fiber.ErrNotFound
		}
		isMember, _ := auth.IsSchoolMember(opts.DB, row.SchoolID, uuid)
		if row.Visibility != "public" && !isMember {
			return fiber.ErrForbidden
		}
		return c.JSON(fiber.Map{"success": true, "data": row})
	})

	// RSVP to event
	app.Post("/v1/events/:id/rsvp", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var e struct{ SchoolID string }
		if err := opts.DB.Get(&e, `SELECT school_id FROM events WHERE id=$1`, id); err != nil {
			return fiber.ErrNotFound
		}
		isMember, _ := auth.IsSchoolMember(opts.DB, e.SchoolID, uid)
		if !isMember {
			return fiber.ErrForbidden
		}
		var body struct {
			Status string `json:"status"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		st := strings.ToLower(strings.TrimSpace(body.Status))
		if st == "" {
			st = "interested"
		}
		if st != "going" && st != "interested" && st != "declined" {
			return fiber.ErrBadRequest
		}
		_, _ = opts.DB.Exec(`INSERT INTO event_attendees (event_id, user_id, status) VALUES ($1,$2,$3) ON CONFLICT (event_id,user_id) DO UPDATE SET status=EXCLUDED.status`, id, uid, st)
		return c.JSON(fiber.Map{"success": true})
	})

	// Update or delete event (admin or creator)
	app.Patch("/v1/events/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var meta struct{ SchoolID, CreatedBy string }
		if err := opts.DB.Get(&meta, `SELECT school_id, created_by_user_id FROM events WHERE id=$1`, id); err != nil {
			return fiber.ErrNotFound
		}
		isAdmin, _ := auth.IsSchoolAdmin(opts.DB, meta.SchoolID, uid)
		if !isAdmin && meta.CreatedBy != uid {
			return fiber.ErrForbidden
		}
		var body struct {
			Title       *string `json:"title"`
			Description *string `json:"description"`
			Location    *string `json:"location"`
			StartsAt    *string `json:"starts_at"`
			EndsAt      *string `json:"ends_at"`
			Visibility  *string `json:"visibility"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		// build dynamic update
		sets := []string{}
		args := []any{}
		if body.Title != nil {
			sets = append(sets, "title=$1")
			args = append(args, *body.Title)
		}
		if body.Description != nil {
			sets = append(sets, "description=$2")
			args = append(args, *body.Description)
		}
		if body.Location != nil {
			sets = append(sets, "location=$3")
			args = append(args, *body.Location)
		}
		if body.StartsAt != nil {
			sets = append(sets, "starts_at=$4")
			args = append(args, *body.StartsAt)
		}
		if body.EndsAt != nil {
			sets = append(sets, "ends_at=$5::timestamptz")
			args = append(args, *body.EndsAt)
		}
		if body.Visibility != nil {
			sets = append(sets, "visibility=$6")
			v := strings.ToLower(strings.TrimSpace(*body.Visibility))
			if v == "" {
				v = "school"
			}
			args = append(args, v)
		}
		if len(sets) == 0 {
			return c.JSON(fiber.Map{"success": true})
		}
		// parameter positions need rebinding; simpler: named style
		_, err = opts.DB.Exec(`UPDATE events SET title=COALESCE($1,title), description=COALESCE($2,description), location=COALESCE($3,location), starts_at=COALESCE($4,starts_at), ends_at=COALESCE($5,ends_at), visibility=COALESCE($6,visibility) WHERE id=$7`, body.Title, body.Description, body.Location, body.StartsAt, body.EndsAt, body.Visibility, id)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	app.Delete("/v1/events/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var meta struct{ SchoolID, CreatedBy string }
		if err := opts.DB.Get(&meta, `SELECT school_id, created_by_user_id FROM events WHERE id=$1`, id); err != nil {
			return fiber.ErrNotFound
		}
		isAdmin, _ := auth.IsSchoolAdmin(opts.DB, meta.SchoolID, uid)
		if !isAdmin && meta.CreatedBy != uid {
			return fiber.ErrForbidden
		}
		_, _ = opts.DB.Exec(`DELETE FROM events WHERE id=$1`, id)
		return c.JSON(fiber.Map{"success": true})
	})

	// List announcements (members can see school + public; non-members see public only)
	app.Get("/v1/schools/:id/announcements", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uuid, _ := getUserID(c)
		sid := c.Params("id")
		isMember, _ := auth.IsSchoolMember(opts.DB, sid, uuid)
		limit := 50
		if v := strings.TrimSpace(c.Query("limit")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}
		offset := 0
		if v := strings.TrimSpace(c.Query("offset")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				offset = n
			}
		}
		type A struct {
			ID, Title, Body, Visibility, Author string
			Pinned                              bool
			CreatedAt                           time.Time
		}
		rows := []A{}
		if isMember {
			_ = opts.DB.Select(&rows, `SELECT a.id, a.title, a.body, a.visibility, a.author_user_id AS author, a.pinned, a.created_at FROM announcements a WHERE a.school_id=$1 ORDER BY a.pinned DESC, a.created_at DESC LIMIT $2 OFFSET $3`, sid, limit, offset)
		} else {
			_ = opts.DB.Select(&rows, `SELECT a.id, a.title, a.body, a.visibility, a.author_user_id AS author, a.pinned, a.created_at FROM announcements a WHERE a.school_id=$1 AND a.visibility='public' ORDER BY a.pinned DESC, a.created_at DESC LIMIT $2 OFFSET $3`, sid, limit, offset)
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// My feed across schools (member-only)
	app.Get("/v1/me/announcements", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		limit := 50
		if v := strings.TrimSpace(c.Query("limit")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}
		offset := 0
		if v := strings.TrimSpace(c.Query("offset")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				offset = n
			}
		}
		type A struct {
			ID, SchoolID, Title, Body, Visibility, Author string
			Pinned                                        bool
			CreatedAt                                     time.Time
		}
		rows := []A{}
		_ = opts.DB.Select(&rows, `SELECT a.id, a.school_id, a.title, a.body, a.visibility, a.author_user_id AS author, a.pinned, a.created_at FROM announcements a JOIN school_members m ON m.school_id=a.school_id AND m.user_id=$1 AND m.status='active' ORDER BY a.pinned DESC, a.created_at DESC LIMIT $2 OFFSET $3`, uid, limit, offset)
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// --- Notifications: email processor (admin-triggered) ---
	app.Get("/v1/admin/notifications/pending", func(c *fiber.Ctx) error {
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) {
			return fiber.ErrForbidden
		}
		var n int
		_ = opts.DB.Get(&n, `SELECT COUNT(*) FROM notifications_queue WHERE status='pending'`)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"pending": n}})
	})

	app.Post("/v1/admin/notifications/process", func(c *fiber.Ctx) error {
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) {
			return fiber.ErrForbidden
		}
		limit := 50
		if v := c.Query("limit"); v != "" {
			if n, e := strconv.Atoi(v); e == nil && n > 0 && n <= 500 {
				limit = n
			}
		}
		rows := []struct {
			ID, Recipient, NType string
			Payload              []byte
		}{}
		if err := opts.DB.Select(&rows, `SELECT id, recipient_user_id AS recipient, ntype, COALESCE(payload,'{}'::jsonb) AS payload FROM notifications_queue WHERE status='pending' ORDER BY created_at ASC LIMIT $1`, limit); err != nil {
			return fiber.ErrInternalServerError
		}
		processed := 0
		for _, r := range rows {
			// derive email, subject, body
			email := ""
			var pl map[string]any
			_ = json.Unmarshal(r.Payload, &pl)
			if v, ok := pl["email"].(string); ok {
				email = strings.TrimSpace(v)
			}
			subject := "Notification"
			body := "You have a new notification."
			switch strings.ToLower(r.NType) {
			case "class_announcement":
				subject = fmt.Sprintf("Class Announcement: %v", pl["title"])
				body = fmt.Sprintf("%v", pl["body"])
			case "school_application_approved":
				subject = "Your school application was approved"
				body = "You can now manage your school."
			case "school_application_rejected":
				subject = "Your school application was rejected"
				body = fmt.Sprintf("Reason: %v", pl["reason"])
			case "tutor_application_approved":
				subject = "Your tutor application was approved"
				body = "You can start creating classes."
			case "tutor_application_rejected":
				subject = "Your tutor application was rejected"
				body = fmt.Sprintf("Reason: %v", pl["reason"])
			case "direct_message":
				subject = "You received a new message"
				body = fmt.Sprintf("From: %v\n%v", pl["from"], pl["preview"])
			case "call_invite":
				subject = "You have an incoming call"
				body = fmt.Sprintf("Join link: %v", pl["url"])
			}
			if email == "" {
				email = fmt.Sprintf("%s@placeholder.local", r.Recipient)
			}
			if err := sendEmail(email, subject, body); err != nil {
				_, _ = opts.DB.Exec(`UPDATE notifications_queue SET status='failed', last_error=$1, updated_at=now() WHERE id=$2`, err.Error(), r.ID)
				continue
			}
			if phone, ok := pl["phone"].(string); ok && strings.TrimSpace(phone) != "" {
				_ = sendSMS(strings.TrimSpace(phone), subject+": "+body)
			}
			// best-effort web push
			var subs []struct{ Endpoint, P256dh, Auth string }
			_ = opts.DB.Select(&subs, `SELECT endpoint, p256dh, auth FROM web_push_subscriptions WHERE user_id=$1`, r.Recipient)
			if len(subs) > 0 && opts.VAPIDPublicKey != "" && opts.VAPIDPrivateKey != "" {
				pay, _ := json.Marshal(fiber.Map{"title": subject, "body": body, "url": "/"})
				for _, s := range subs {
					resp, err := webpush.SendNotification(pay, &webpush.Subscription{Endpoint: s.Endpoint, Keys: webpush.Keys{P256dh: s.P256dh, Auth: s.Auth}}, &webpush.Options{VAPIDPublicKey: opts.VAPIDPublicKey, VAPIDPrivateKey: opts.VAPIDPrivateKey, TTL: 60})
					if err == nil && resp != nil {
						io.Copy(io.Discard, resp.Body)
						resp.Body.Close()
					}
				}
			}
			_, _ = opts.DB.Exec(`UPDATE notifications_queue SET status='sent', updated_at=now() WHERE id=$1`, r.ID)
			processed++
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"processed": processed}})
	})

	// Conversation archiving
	app.Post("/v1/messages/threads/:id/archive", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var has bool
		_ = opts.DB.Get(&has, `SELECT EXISTS (SELECT 1 FROM direct_participants WHERE thread_id=$1 AND user_id=$2)`, id, uid)
		if !has {
			return fiber.ErrForbidden
		}
		if _, err := opts.DB.Exec(`UPDATE direct_threads SET archived_at=now() WHERE id=$1 AND archived_at IS NULL`, id); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		logAudit(uid, "messages_archive", "thread", id, nil)
		return c.JSON(fiber.Map{"success": true})
	})

	// --- Freemium Features ---
	// List effective features for current user (optionally for a specific school)
	app.Get("/v1/features", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := strings.TrimSpace(c.Query("school_id"))
		features := map[string]bool{}
		// School-level flags
		if sid != "" {
			rows := []struct {
				Key     string `db:"feature_key"`
				Enabled bool   `db:"enabled"`
			}{}
			_ = opts.DB.Select(&rows, `SELECT feature_key, enabled FROM school_feature_flags WHERE school_id=$1`, sid)
			for _, r := range rows {
				features[r.Key] = r.Enabled
			}
		}
		// User overrides
		rows := []struct {
			Key     string `db:"feature_key"`
			Enabled bool   `db:"enabled"`
		}{}
		_ = opts.DB.Select(&rows, `SELECT feature_key, enabled FROM user_feature_flags WHERE user_id=$1`, uid)
		for _, r := range rows {
			features[r.Key] = r.Enabled
		}
		return c.JSON(fiber.Map{"success": true, "data": features})
	})

	// --- Grading Scales ---
	// Create grading scale with entries (admin)
	app.Post("/v1/schools/:id/grading-scales", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			Name      string `json:"name"`
			IsDefault bool   `json:"is_default"`
			Entries   []struct {
				Min    float64  `json:"min"`
				Max    float64  `json:"max"`
				Letter string   `json:"letter"`
				Points *float64 `json:"points"`
			} `json:"entries"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Name) == "" || len(body.Entries) == 0 {
			return fiber.ErrBadRequest
		}
		var scaleID string
		if err := opts.DB.Get(&scaleID, `INSERT INTO grading_scales (school_id, name, is_default, active) VALUES ($1,$2,$3,TRUE) RETURNING id`, sid, body.Name, body.IsDefault); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		for _, e := range body.Entries {
			_, _ = opts.DB.Exec(`INSERT INTO grading_scale_entries (scale_id, min_percent, max_percent, letter, points) VALUES ($1,$2,$3,$4,$5)`, scaleID, e.Min, e.Max, e.Letter, e.Points)
		}
		if body.IsDefault {
			_, _ = opts.DB.Exec(`UPDATE grading_scales SET is_default=FALSE WHERE school_id=$1 AND id<>$2`, sid, scaleID)
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": scaleID}})
	})

	app.Get("/v1/schools/:id/grading-scales", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		sid := c.Params("id")
		rows := []struct {
			ID, Name          string
			IsDefault, Active bool
		}{}
		_ = opts.DB.Select(&rows, `SELECT id, name, is_default, active FROM grading_scales WHERE school_id=$1 ORDER BY created_at DESC`, sid)
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	app.Patch("/v1/grading-scales/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var sid string
		_ = opts.DB.Get(&sid, `SELECT school_id FROM grading_scales WHERE id=$1`, id)
		if sid == "" {
			return fiber.ErrNotFound
		}
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			Name      *string `json:"name"`
			IsDefault *bool   `json:"is_default"`
			Active    *bool   `json:"active"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		_, _ = opts.DB.Exec(`UPDATE grading_scales SET name=COALESCE($1,name), is_default=COALESCE($2,is_default), active=COALESCE($3,active), updated_at=now() WHERE id=$4`, body.Name, body.IsDefault, body.Active, id)
		if body.IsDefault != nil && *body.IsDefault {
			_, _ = opts.DB.Exec(`UPDATE grading_scales SET is_default=FALSE WHERE school_id=$1 AND id<>$2`, sid, id)
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// Set school feature flag (admin)
	app.Post("/v1/schools/:id/features", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			FeatureKey string `json:"feature_key"`
			Enabled    *bool  `json:"enabled"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.FeatureKey) == "" || body.Enabled == nil {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`INSERT INTO school_feature_flags (school_id, feature_key, enabled) VALUES ($1,$2,$3)
			ON CONFLICT (school_id, feature_key) DO UPDATE SET enabled=EXCLUDED.enabled`, sid, body.FeatureKey, *body.Enabled)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// --- Facilities & Booking ---
	// Create facility (admin)
	app.Post("/v1/schools/:id/facilities", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			Name       string         `json:"name"`
			Location   *string        `json:"location"`
			Capacity   *int           `json:"capacity"`
			Attributes map[string]any `json:"attributes"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Name) == "" {
			return fiber.ErrBadRequest
		}
		var id string
		aBytes, _ := json.Marshal(body.Attributes)
		if err := opts.DB.Get(&id, `INSERT INTO facilities (school_id,name,location,capacity,attributes) VALUES ($1,$2,$3,$4,COALESCE($5,'{}'::jsonb)) RETURNING id`, sid, body.Name, body.Location, body.Capacity, nullIfEmptyJSON(aBytes)); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": id}})
	})

	// List facilities (admin or member)
	app.Get("/v1/schools/:id/facilities", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uuid, _ := getUserID(c)
		sid := c.Params("id")
		isMember, _ := auth.IsSchoolMember(opts.DB, sid, uuid)
		if !isMember {
			return fiber.ErrForbidden
		}
		rows := []struct {
			ID, Name, Location string
			Capacity           *int
			Active             bool
		}{}
		_ = opts.DB.Select(&rows, `SELECT id, name, COALESCE(location,'') AS location, capacity, active FROM facilities WHERE school_id=$1 AND active=true ORDER BY name`, sid)
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Update facility (admin)
	app.Patch("/v1/facilities/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var sid string
		_ = opts.DB.Get(&sid, `SELECT school_id FROM facilities WHERE id=$1`, id)
		if sid == "" {
			return fiber.ErrNotFound
		}
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			Name     *string `json:"name"`
			Location *string `json:"location"`
			Capacity *int    `json:"capacity"`
			Active   *bool   `json:"active"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`UPDATE facilities SET name=COALESCE($1,name), location=COALESCE($2,location), capacity=COALESCE($3,capacity), active=COALESCE($4,active), updated_at=now() WHERE id=$5`, body.Name, body.Location, body.Capacity, body.Active, id)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// Facility availability (booked slots)
	app.Get("/v1/facilities/:id/availability", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		id := c.Params("id")
		from := strings.TrimSpace(c.Query("from"))
		to := strings.TrimSpace(c.Query("to"))
		rows := []struct {
			StartsAt time.Time `db:"starts_at" json:"starts_at"`
			EndsAt   time.Time `db:"ends_at" json:"ends_at"`
		}{}
		q := `SELECT starts_at, ends_at FROM facility_bookings WHERE facility_id=$1 AND status='booked'`
		args := []any{id}
		if from != "" {
			q += " AND starts_at >= $2"
			args = append(args, from)
		}
		if to != "" {
			if len(args) == 1 {
				q += " AND ends_at <= $2"
			} else {
				q += " AND ends_at <= $3"
			}
			args = append(args, to)
		}
		_ = opts.DB.Select(&rows, q, args...)
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Book a facility (member)
	app.Post("/v1/facilities/:id/book", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var sid string
		_ = opts.DB.Get(&sid, `SELECT school_id FROM facilities WHERE id=$1 AND active=true`, id)
		if sid == "" {
			return fiber.ErrNotFound
		}
		isMember, _ := auth.IsSchoolMember(opts.DB, sid, uid)
		if !isMember {
			return fiber.ErrForbidden
		}
		var body struct {
			Title       string  `json:"title"`
			Description *string `json:"description"`
			StartsAt    string  `json:"starts_at"`
			EndsAt      string  `json:"ends_at"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.StartsAt) == "" || strings.TrimSpace(body.EndsAt) == "" {
			return fiber.ErrBadRequest
		}
		// conflict check
		var conflict bool
		_ = opts.DB.Get(&conflict, `SELECT EXISTS (SELECT 1 FROM facility_bookings WHERE facility_id=$1 AND status='booked' AND NOT (ends_at <= $2 OR starts_at >= $3))`, id, body.StartsAt, body.EndsAt)
		if conflict {
			return c.Status(409).JSON(fiber.Map{"success": false, "message": "time slot not available"})
		}
		var bid string
		if err := opts.DB.Get(&bid, `INSERT INTO facility_bookings (facility_id, user_id, title, description, starts_at, ends_at) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, id, uid, body.Title, body.Description, body.StartsAt, body.EndsAt); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": bid}})
	})

	// My bookings
	app.Get("/v1/me/bookings", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := strings.TrimSpace(c.Query("school_id"))
		rows := []struct {
			ID, FacilityID, Title string
			StartsAt, EndsAt      time.Time
			Status                string
		}{}
		if sid == "" {
			_ = opts.DB.Select(&rows, `SELECT id, facility_id, COALESCE(title,'') AS title, starts_at, ends_at, status FROM facility_bookings WHERE user_id=$1 ORDER BY starts_at DESC`, uid)
		} else {
			_ = opts.DB.Select(&rows, `SELECT b.id, b.facility_id, COALESCE(b.title,'') AS title, b.starts_at, b.ends_at, b.status FROM facility_bookings b JOIN facilities f ON f.id=b.facility_id WHERE b.user_id=$1 AND f.school_id=$2 ORDER BY b.starts_at DESC`, uid, sid)
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Cancel booking (booker or admin)
	app.Delete("/v1/bookings/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var meta struct{ FacilityID, UserID, SchoolID string }
		_ = opts.DB.Get(&meta, `SELECT b.facility_id, b.user_id, f.school_id FROM facility_bookings b JOIN facilities f ON f.id=b.facility_id WHERE b.id=$1`, id)
		if meta.FacilityID == "" {
			return fiber.ErrNotFound
		}
		isAdmin, _ := auth.IsSchoolAdmin(opts.DB, meta.SchoolID, uid)
		if uid != meta.UserID && !isAdmin {
			return fiber.ErrForbidden
		}
		_, _ = opts.DB.Exec(`UPDATE facility_bookings SET status='canceled', updated_at=now() WHERE id=$1`, id)
		return c.JSON(fiber.Map{"success": true})
	})

	// --- Library Management ---
	// Create book (admin)
	app.Post("/v1/schools/:id/library/books", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			ISBN, Title, Author, Subject, Description, CoverURL *string
			Tags                                                map[string]any
		}
		if err := c.BodyParser(&body); err != nil || body.Title == nil || strings.TrimSpace(*body.Title) == "" {
			return fiber.ErrBadRequest
		}
		var id string
		tb, _ := json.Marshal(body.Tags)
		if err := opts.DB.Get(&id, `INSERT INTO library_books (school_id, isbn, title, author, subject, tags, description, cover_url) VALUES ($1,$2,$3,$4,$5,COALESCE($6,'{}'::jsonb),$7,$8) RETURNING id`, sid, body.ISBN, body.Title, body.Author, body.Subject, nullIfEmptyJSON(tb), body.Description, body.CoverURL); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": id}})
	})

	// Search/list books (members)
	app.Get("/v1/schools/:id/library/books", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uuid, _ := getUserID(c)
		sid := c.Params("id")
		isMember, _ := auth.IsSchoolMember(opts.DB, sid, uuid)
		if !isMember {
			return fiber.ErrForbidden
		}
		q := strings.TrimSpace(c.Query("q"))
		limit := 50
		if v := c.Query("limit"); v != "" {
			if n, e := strconv.Atoi(v); e == nil && n > 0 && n <= 200 {
				limit = n
			}
		}
		offset := 0
		if v := c.Query("offset"); v != "" {
			if n, e := strconv.Atoi(v); e == nil && n >= 0 {
				offset = n
			}
		}
		rows := []struct {
			ID, Title, Author, Subject string
			CoverURL                   *string
		}{}
		if q == "" {
			_ = opts.DB.Select(&rows, `SELECT id, title, COALESCE(author,'') AS author, COALESCE(subject,'') AS subject, cover_url FROM library_books WHERE school_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, sid, limit, offset)
		} else {
			_ = opts.DB.Select(&rows, `SELECT id, title, COALESCE(author,'') AS author, COALESCE(subject,'') AS subject, cover_url FROM library_books WHERE school_id=$1 AND to_tsvector('english', coalesce(title,'') || ' ' || coalesce(author,'')) @@ plainto_tsquery('english', $2) ORDER BY created_at DESC LIMIT $3 OFFSET $4`, sid, q, limit, offset)
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Manage copies (admin create, list)
	app.Post("/v1/library/books/:id/copies", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		bid := c.Params("id")
		var sid string
		_ = opts.DB.Get(&sid, `SELECT school_id FROM library_books WHERE id=$1`, bid)
		if sid == "" {
			return fiber.ErrNotFound
		}
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct{ Barcode *string }
		_ = c.BodyParser(&body)
		var id string
		if err := opts.DB.Get(&id, `INSERT INTO library_copies (book_id, barcode) VALUES ($1, NULLIF($2,'')) RETURNING id`, bid, coalesceString(body.Barcode)); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": id}})
	})
	app.Get("/v1/library/books/:id/copies", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uuid, _ := getUserID(c)
		bid := c.Params("id")
		var sid string
		_ = opts.DB.Get(&sid, `SELECT school_id FROM library_books WHERE id=$1`, bid)
		if sid == "" {
			return fiber.ErrNotFound
		}
		isMember, _ := auth.IsSchoolMember(opts.DB, sid, uuid)
		if !isMember {
			return fiber.ErrForbidden
		}
		rows := []struct{ ID, Status, Barcode string }{}
		_ = opts.DB.Select(&rows, `SELECT id, status, COALESCE(barcode,'') AS barcode FROM library_copies WHERE book_id=$1 ORDER BY status`, bid)
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Loans
	app.Post("/v1/library/copies/:id/loan", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		cid := c.Params("id")
		// Ensure copy belongs to a school where user is member and is available
		var row struct{ SchoolID, Status string }
		_ = opts.DB.Get(&row, `SELECT b.school_id, c.status FROM library_copies c JOIN library_books b ON b.id=c.book_id WHERE c.id=$1`, cid)
		if row.SchoolID == "" {
			return fiber.ErrNotFound
		}
		isMember, _ := auth.IsSchoolMember(opts.DB, row.SchoolID, uid)
		if !isMember || strings.ToLower(row.Status) != "available" {
			return fiber.ErrForbidden
		}
		// Create loan (14-day default)
		due := time.Now().Add(14 * 24 * time.Hour)
		var lid string
		if err := opts.DB.Get(&lid, `INSERT INTO library_loans (copy_id, user_id, status, due_at) VALUES ($1,$2,'active',$3) RETURNING id`, cid, uid, due); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		_, _ = opts.DB.Exec(`UPDATE library_copies SET status='loaned' WHERE id=$1`, cid)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": lid, "due_at": due.UTC().Format(time.RFC3339)}})
	})

	app.Post("/v1/library/loans/:id/return", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		lid := c.Params("id")
		var meta struct{ CopyID, UserID string }
		_ = opts.DB.Get(&meta, `SELECT copy_id, user_id FROM library_loans WHERE id=$1 AND status='active'`, lid)
		if meta.CopyID == "" {
			return fiber.ErrNotFound
		}
		// Allow returning by borrower or school admin
		var sid string
		_ = opts.DB.Get(&sid, `SELECT b.school_id FROM library_copies c JOIN library_books b ON b.id=c.book_id WHERE c.id=$1`, meta.CopyID)
		isAdmin, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if uid != meta.UserID && !isAdmin {
			return fiber.ErrForbidden
		}
		_, _ = opts.DB.Exec(`UPDATE library_loans SET status='returned', returned_at=now() WHERE id=$1`, lid)
		_, _ = opts.DB.Exec(`UPDATE library_copies SET status='available' WHERE id=$1`, meta.CopyID)
		return c.JSON(fiber.Map{"success": true})
	})

	app.Get("/v1/me/library/loans", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		status := strings.ToLower(strings.TrimSpace(c.Query("status")))
		rows := []struct {
			ID, CopyID, Status string
			LoanedAt, DueAt    time.Time
			ReturnedAt         *time.Time
		}{}
		if status == "" || status == "active" {
			_ = opts.DB.Select(&rows, `SELECT id, copy_id, status, loaned_at, due_at, returned_at FROM library_loans WHERE user_id=$1 AND status<>'returned' ORDER BY loaned_at DESC`, uid)
		} else {
			_ = opts.DB.Select(&rows, `SELECT id, copy_id, status, loaned_at, due_at, returned_at FROM library_loans WHERE user_id=$1 ORDER BY loaned_at DESC`, uid)
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// --- Inventory Tracking ---
	// Create inventory item (admin)
	app.Post("/v1/schools/:id/inventory/items", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			SKU, Name, Category *string
			Attributes          map[string]any
		}
		if err := c.BodyParser(&body); err != nil || body.Name == nil || strings.TrimSpace(*body.Name) == "" {
			return fiber.ErrBadRequest
		}
		var id string
		ab, _ := json.Marshal(body.Attributes)
		if err := opts.DB.Get(&id, `INSERT INTO inventory_items (school_id, sku, name, category, attributes) VALUES ($1,$2,$3,$4,COALESCE($5,'{}'::jsonb)) RETURNING id`, sid, body.SKU, body.Name, body.Category, nullIfEmptyJSON(ab)); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": id}})
	})

	// List inventory items (members) with computed stock
	app.Get("/v1/schools/:id/inventory/items", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uuid, _ := getUserID(c)
		sid := c.Params("id")
		isMember, _ := auth.IsSchoolMember(opts.DB, sid, uuid)
		if !isMember {
			return fiber.ErrForbidden
		}
		q := strings.TrimSpace(c.Query("q"))
		rows := []struct{ ID, Name, Category string }{}
		if q == "" {
			_ = opts.DB.Select(&rows, `SELECT id, name, COALESCE(category,'') AS category FROM inventory_items WHERE school_id=$1 ORDER BY created_at DESC`, sid)
		} else {
			_ = opts.DB.Select(&rows, `SELECT id, name, COALESCE(category,'') AS category FROM inventory_items WHERE school_id=$1 AND to_tsvector('english', coalesce(name,'') || ' ' || coalesce(category,'')) @@ plainto_tsquery('english',$2) ORDER BY created_at DESC`, sid, q)
		}
		out := []fiber.Map{}
		for _, r := range rows {
			var stock int
			_ = opts.DB.Get(&stock, `SELECT COALESCE(SUM(change),0) FROM inventory_movements WHERE item_id=$1`, r.ID)
			out = append(out, fiber.Map{"id": r.ID, "name": r.Name, "category": r.Category, "stock": stock})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})

	// --- Hostel / Boarding ---
	// Create hostel (admin)
	app.Post("/v1/schools/:id/hostels", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			Name     string  `json:"name"`
			Location *string `json:"location"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Name) == "" {
			return fiber.ErrBadRequest
		}
		var hid string
		if err := opts.DB.Get(&hid, `INSERT INTO hostels (school_id, name, location) VALUES ($1,$2,$3) RETURNING id`, sid, body.Name, body.Location); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": hid}})
	})

	// Add room (admin)
	app.Post("/v1/hostels/:id/rooms", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		hid := c.Params("id")
		var sid string
		_ = opts.DB.Get(&sid, `SELECT school_id FROM hostels WHERE id=$1`, hid)
		if sid == "" {
			return fiber.ErrNotFound
		}
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			RoomNo   string  `json:"room_no"`
			Capacity *int    `json:"capacity"`
			Gender   *string `json:"gender"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.RoomNo) == "" {
			return fiber.ErrBadRequest
		}
		var rid string
		if err := opts.DB.Get(&rid, `INSERT INTO hostel_rooms (hostel_id, room_no, capacity, gender) VALUES ($1,$2,COALESCE($3,1),$4) RETURNING id`, hid, body.RoomNo, body.Capacity, body.Gender); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": rid}})
	})

	// List hostels with occupancy summary (members)
	app.Get("/v1/schools/:id/hostels", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uuid, _ := getUserID(c)
		sid := c.Params("id")
		isMember, _ := auth.IsSchoolMember(opts.DB, sid, uuid)
		if !isMember {
			return fiber.ErrForbidden
		}
		hostels := []struct{ ID, Name, Location string }{}
		_ = opts.DB.Select(&hostels, `SELECT id, name, COALESCE(location,'') AS location FROM hostels WHERE school_id=$1 ORDER BY name`, sid)
		out := []fiber.Map{}
		for _, h := range hostels {
			var rooms int
			var beds int
			var occupied int
			_ = opts.DB.Get(&rooms, `SELECT COUNT(*) FROM hostel_rooms WHERE hostel_id=$1`, h.ID)
			_ = opts.DB.Get(&beds, `SELECT COALESCE(SUM(capacity),0) FROM hostel_rooms WHERE hostel_id=$1`, h.ID)
			_ = opts.DB.Get(&occupied, `SELECT COALESCE(SUM(1),0) FROM hostel_allocations a JOIN hostel_rooms r ON r.id=a.room_id WHERE r.hostel_id=$1 AND a.status='active'`, h.ID)
			out = append(out, fiber.Map{"id": h.ID, "name": h.Name, "location": h.Location, "rooms": rooms, "beds": beds, "occupied": occupied})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})

	// Allocate student to a room (admin)
	app.Post("/v1/rooms/:id/allocate", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		rid := c.Params("id")
		var meta struct {
			HostelID, SchoolID string
			Capacity           int
		}
		_ = opts.DB.Get(&meta, `SELECT r.hostel_id, h.school_id, r.capacity FROM hostel_rooms r JOIN hostels h ON h.id=r.hostel_id WHERE r.id=$1`, rid)
		if meta.SchoolID == "" {
			return fiber.ErrNotFound
		}
		ok, _ := auth.IsSchoolAdmin(opts.DB, meta.SchoolID, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			StudentUserID string `json:"student_user_id"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.StudentUserID) == "" {
			return fiber.ErrBadRequest
		}
		// Capacity check
		var occ int
		_ = opts.DB.Get(&occ, `SELECT COUNT(*) FROM hostel_allocations WHERE room_id=$1 AND status='active'`, rid)
		if occ >= meta.Capacity {
			return c.Status(409).JSON(fiber.Map{"success": false, "message": "room full"})
		}
		_, err = opts.DB.Exec(`INSERT INTO hostel_allocations (room_id, student_user_id) VALUES ($1,$2)`, rid, body.StudentUserID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// Checkout (student or admin)
	app.Post("/v1/allocations/:id/checkout", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		aid := c.Params("id")
		var meta struct{ RoomID, Student string }
		_ = opts.DB.Get(&meta, `SELECT room_id, student_user_id FROM hostel_allocations WHERE id=$1 AND status='active'`, aid)
		if meta.RoomID == "" {
			return fiber.ErrNotFound
		}
		var sid string
		_ = opts.DB.Get(&sid, `SELECT h.school_id FROM hostel_rooms r JOIN hostels h ON h.id=r.hostel_id WHERE r.id=$1`, meta.RoomID)
		isAdmin, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if uid != meta.Student && !isAdmin {
			return fiber.ErrForbidden
		}
		_, _ = opts.DB.Exec(`UPDATE hostel_allocations SET status='checked_out', end_date=CURRENT_DATE WHERE id=$1`, aid)
		return c.JSON(fiber.Map{"success": true})
	})

	// My hostel allocation
	app.Get("/v1/me/hostel", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		row := struct{ AllocationID, RoomID, HostelID, SchoolID, RoomNo, HostelName string }{}
		_ = opts.DB.Get(&row, `SELECT a.id AS allocation_id, r.id AS room_id, h.id AS hostel_id, h.school_id AS school_id, r.room_no, h.name AS hostel_name
			FROM hostel_allocations a JOIN hostel_rooms r ON r.id=a.room_id JOIN hostels h ON h.id=r.hostel_id WHERE a.student_user_id=$1 AND a.status='active' LIMIT 1`, uid)
		if row.AllocationID == "" {
			return c.JSON(fiber.Map{"success": true, "data": nil})
		}
		return c.JSON(fiber.Map{"success": true, "data": row})
	})

	// --- Transportation ---
	// Create route (admin)
	app.Post("/v1/schools/:id/transport/routes", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Name) == "" {
			return fiber.ErrBadRequest
		}
		var rid string
		if err := opts.DB.Get(&rid, `INSERT INTO transport_routes (school_id, name) VALUES ($1,$2) RETURNING id`, sid, body.Name); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": rid}})
	})

	// List routes with stops (members)
	app.Get("/v1/schools/:id/transport/routes", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uuid, _ := getUserID(c)
		sid := c.Params("id")
		isMember, _ := auth.IsSchoolMember(opts.DB, sid, uuid)
		if !isMember {
			return fiber.ErrForbidden
		}
		routes := []struct{ ID, Name string }{}
		_ = opts.DB.Select(&routes, `SELECT id, name FROM transport_routes WHERE school_id=$1 ORDER BY name`, sid)
		out := []fiber.Map{}
		for _, r := range routes {
			stops := []struct {
				ID, Name string
				Lat, Lng *float64
				Order    int
			}{}
			_ = opts.DB.Select(&stops, `SELECT id, name, lat::float8 AS lat, lng::float8 AS lng, order_index AS "order" FROM transport_stops WHERE route_id=$1 ORDER BY order_index`, r.ID)
			out = append(out, fiber.Map{"id": r.ID, "name": r.Name, "stops": stops})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})

	// Add stop (admin)
	app.Post("/v1/transport/routes/:id/stops", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		rid := c.Params("id")
		var sid string
		_ = opts.DB.Get(&sid, `SELECT school_id FROM transport_routes WHERE id=$1`, rid)
		if sid == "" {
			return fiber.ErrNotFound
		}
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			Name       string   `json:"name"`
			Lat        *float64 `json:"lat"`
			Lng        *float64 `json:"lng"`
			OrderIndex *int     `json:"order_index"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Name) == "" {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`INSERT INTO transport_stops (route_id, name, lat, lng, order_index) VALUES ($1,$2,$3,$4,COALESCE($5,0))`, rid, body.Name, body.Lat, body.Lng, body.OrderIndex)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// Assign student to route/stop (admin)
	app.Post("/v1/transport/routes/:id/assign", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		rid := c.Params("id")
		var sid string
		_ = opts.DB.Get(&sid, `SELECT school_id FROM transport_routes WHERE id=$1`, rid)
		if sid == "" {
			return fiber.ErrNotFound
		}
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			StudentUserID string `json:"student_user_id"`
			StopID        string `json:"stop_id"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.StudentUserID) == "" || strings.TrimSpace(body.StopID) == "" {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`INSERT INTO transport_assignments (route_id, stop_id, student_user_id) VALUES ($1,$2,$3) ON CONFLICT (route_id, student_user_id, status) WHERE status='active' DO NOTHING`, rid, body.StopID, body.StudentUserID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// My transport assignments (student)
	app.Get("/v1/me/transport/assignments", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		rows := []struct{ RouteID, StopID, RouteName, StopName string }{}
		_ = opts.DB.Select(&rows, `SELECT a.route_id, a.stop_id, r.name AS route_name, s.name AS stop_name FROM transport_assignments a JOIN transport_routes r ON r.id=a.route_id JOIN transport_stops s ON s.id=a.stop_id WHERE a.student_user_id=$1 AND a.status='active'`, uid)
		out := []fiber.Map{}
		for _, r := range rows {
			out = append(out, fiber.Map{"route_id": r.RouteID, "route_name": r.RouteName, "stop_id": r.StopID, "stop_name": r.StopName})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})

	// Schedule trip (admin)
	app.Post("/v1/transport/routes/:id/trips", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		rid := c.Params("id")
		var sid string
		_ = opts.DB.Get(&sid, `SELECT school_id FROM transport_routes WHERE id=$1`, rid)
		if sid == "" {
			return fiber.ErrNotFound
		}
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			StartTime string `json:"start_time"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.StartTime) == "" {
			return fiber.ErrBadRequest
		}
		var tid string
		if err := opts.DB.Get(&tid, `INSERT INTO transport_trips (route_id, start_time) VALUES ($1,$2) RETURNING id`, rid, body.StartTime); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": tid}})
	})

	// Trip manifest (admin)
	app.Get("/v1/transport/trips/:id/manifest", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		tid := c.Params("id")
		var rid, sid string
		_ = opts.DB.Get(&rid, `SELECT route_id FROM transport_trips WHERE id=$1`, tid)
		if rid == "" {
			return fiber.ErrNotFound
		}
		_ = opts.DB.Get(&sid, `SELECT school_id FROM transport_routes WHERE id=$1`, rid)
		isAdmin, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !isAdmin {
			return fiber.ErrForbidden
		}
		rows := []struct{ Student, StopName string }{}
		_ = opts.DB.Select(&rows, `SELECT a.student_user_id AS student, s.name AS stop_name FROM transport_assignments a JOIN transport_stops s ON s.id=a.stop_id WHERE a.route_id=$1 AND a.status='active' ORDER BY s.order_index`, rid)
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Check-in (admin)
	app.Post("/v1/transport/trips/:id/checkin", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		tid := c.Params("id")
		var rid, sid string
		_ = opts.DB.Get(&rid, `SELECT route_id FROM transport_trips WHERE id=$1`, tid)
		if rid == "" {
			return fiber.ErrNotFound
		}
		_ = opts.DB.Get(&sid, `SELECT school_id FROM transport_routes WHERE id=$1`, rid)
		isAdmin, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !isAdmin {
			return fiber.ErrForbidden
		}
		var body struct {
			StudentUserID string `json:"student_user_id"`
			Event         string `json:"event"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		ev := strings.ToLower(strings.TrimSpace(body.Event))
		if ev != "pickup" && ev != "dropoff" {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`INSERT INTO transport_checkins (trip_id, student_user_id, event) VALUES ($1,$2,$3)`, tid, body.StudentUserID, ev)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// Adjust stock (admin): change can be positive (receive) or negative (consume)
	app.Post("/v1/inventory/items/:id/movements", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var sid string
		_ = opts.DB.Get(&sid, `SELECT school_id FROM inventory_items WHERE id=$1`, id)
		if sid == "" {
			return fiber.ErrNotFound
		}
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !ok {
			return fiber.ErrForbidden
		}
		var body struct {
			Change   int     `json:"change"`
			Reason   *string `json:"reason"`
			Location *string `json:"location"`
		}
		if err := c.BodyParser(&body); err != nil || body.Change == 0 {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`INSERT INTO inventory_movements (item_id, location, change, reason, user_id) VALUES ($1,$2,$3,$4,$5)`, id, body.Location, body.Change, body.Reason, uid)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// Stock by location
	app.Get("/v1/inventory/items/:id/stock", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uuid, _ := getUserID(c)
		id := c.Params("id")
		var sid string
		_ = opts.DB.Get(&sid, `SELECT school_id FROM inventory_items WHERE id=$1`, id)
		if sid == "" {
			return fiber.ErrNotFound
		}
		isMember, _ := auth.IsSchoolMember(opts.DB, sid, uuid)
		if !isMember {
			return fiber.ErrForbidden
		}
		rows := []struct {
			Location *string
			Stock    int
		}{}
		_ = opts.DB.Select(&rows, `SELECT NULLIF(location,'') AS location, COALESCE(SUM(change),0) AS stock FROM inventory_movements WHERE item_id=$1 GROUP BY location`, id)
		out := []fiber.Map{}
		for _, r := range rows {
			out = append(out, fiber.Map{"location": r.Location, "stock": r.Stock})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})

	// --- Study Groups ---
	isGroupMod := func(gid, uid string) bool {
		if gid == "" || uid == "" {
			return false
		}
		// owner/moderator
		var role string
		_ = opts.DB.Get(&role, `SELECT role FROM study_group_members WHERE group_id=$1 AND user_id=$2 AND status='active'`, gid, uid)
		if role == "owner" || role == "moderator" {
			return true
		}
		// class tutor/admin
		var cid string
		_ = opts.DB.Get(&cid, `SELECT class_id FROM study_groups WHERE id=$1`, gid)
		if cid != "" {
			ok, _ := auth.IsClassTutor(opts.DB, cid, uid)
			if ok {
				return true
			}
			var sid *string
			_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, cid)
			if sid != nil && *sid != "" {
				ok, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
				if ok {
					return true
				}
			}
		}
		return false
	}
	// List groups for a class (members only)
	app.Get("/v1/classes/:id/groups", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		cid := c.Params("id")
		if !isClassMember(cid, uid) {
			return fiber.ErrForbidden
		}
		type row struct {
			ID, Title, CreatedBy string
			CreatedAt            time.Time
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT id, title, created_by_user_id AS created_by, created_at FROM study_groups WHERE class_id=$1 ORDER BY created_at DESC`, cid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	// Create group (any class member)
	app.Post("/v1/classes/:id/groups", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		cid := c.Params("id")
		if !isClassMember(cid, uid) {
			return fiber.ErrForbidden
		}
		var body struct {
			Title string `json:"title"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Title) == "" {
			return fiber.ErrBadRequest
		}
		var gid string
		if err := opts.DB.Get(&gid, `INSERT INTO study_groups (class_id, title, created_by_user_id) VALUES ($1,$2,$3) RETURNING id`, cid, strings.TrimSpace(body.Title), uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		_, _ = opts.DB.Exec(`INSERT INTO study_group_members (group_id, user_id, role) VALUES ($1,$2,'owner') ON CONFLICT DO NOTHING`, gid, uid)
		return c.Status(201).JSON(fiber.Map{"success": true, "data": map[string]string{"id": gid}})
	})
	// Join/Leave
	app.Post("/v1/groups/:id/join", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		gid := c.Params("id")
		var cid string
		if err := opts.DB.Get(&cid, `SELECT class_id FROM study_groups WHERE id=$1`, gid); err != nil {
			return fiber.ErrNotFound
		}
		if !isClassMember(cid, uid) {
			return fiber.ErrForbidden
		}
		_, err = opts.DB.Exec(`INSERT INTO study_group_members (group_id, user_id) VALUES ($1,$2) ON CONFLICT (group_id, user_id) DO UPDATE SET status='active'`, gid, uid)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Post("/v1/groups/:id/leave", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		gid := c.Params("id")
		_, err = opts.DB.Exec(`UPDATE study_group_members SET status='left' WHERE group_id=$1 AND user_id=$2`, gid, uid)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// Messages
	app.Get("/v1/groups/:id/messages", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		gid := c.Params("id")
		var cid string
		if err := opts.DB.Get(&cid, `SELECT class_id FROM study_groups WHERE id=$1`, gid); err != nil {
			return fiber.ErrNotFound
		}
		if !isClassMember(cid, uid) {
			return fiber.ErrForbidden
		}
		type msg struct {
			ID, Sender, Body string
			Attachments      json.RawMessage
			CreatedAt        time.Time
		}
		rows := []msg{}
		if err := opts.DB.Select(&rows, `SELECT id, sender_user_id AS sender, COALESCE(body,'') AS body, COALESCE(attachments,'null'::jsonb) AS attachments, created_at FROM study_group_messages WHERE group_id=$1 ORDER BY created_at ASC LIMIT 500`, gid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/groups/:id/messages", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		gid := c.Params("id")
		var cid string
		if err := opts.DB.Get(&cid, `SELECT class_id FROM study_groups WHERE id=$1`, gid); err != nil {
			return fiber.ErrNotFound
		}
		if !isClassMember(cid, uid) {
			return fiber.ErrForbidden
		}
		var body struct {
			Body        *string         `json:"body"`
			Attachments json.RawMessage `json:"attachments"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := opts.DB.Get(&out, `INSERT INTO study_group_messages (group_id, sender_user_id, body, attachments) VALUES ($1,$2,$3,$4) RETURNING id`, gid, uid, body.Body, nullIfEmptyJSON(body.Attachments)); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})
	// List members
	app.Get("/v1/groups/:id/members", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		gid := c.Params("id")
		var cid string
		if err := opts.DB.Get(&cid, `SELECT class_id FROM study_groups WHERE id=$1`, gid); err != nil {
			return fiber.ErrNotFound
		}
		if !isClassMember(cid, uid) {
			return fiber.ErrForbidden
		}
		type row struct{ UserID, Role, Status string }
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT user_id, role, status FROM study_group_members WHERE group_id=$1 ORDER BY role DESC, user_id`, gid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	// Set role (owner/mod/class tutor/admin)
	app.Post("/v1/groups/:id/members/:userId/role", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		actor, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		gid := c.Params("id")
		target := c.Params("userId")
		if !isGroupMod(gid, actor) {
			return fiber.ErrForbidden
		}
		var body struct {
			Role string `json:"role"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		role := strings.ToLower(strings.TrimSpace(body.Role))
		if role != "member" && role != "moderator" && role != "owner" {
			return fiber.ErrBadRequest
		}
		if _, err := opts.DB.Exec(`UPDATE study_group_members SET role=$1 WHERE group_id=$2 AND user_id=$3`, role, gid, target); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// Ban/unban
	app.Post("/v1/groups/:id/members/:userId/ban", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		actor, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		gid := c.Params("id")
		target := c.Params("userId")
		if !isGroupMod(gid, actor) {
			return fiber.ErrForbidden
		}
		if _, err := opts.DB.Exec(`UPDATE study_group_members SET status='banned' WHERE group_id=$1 AND user_id=$2`, gid, target); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Post("/v1/groups/:id/members/:userId/unban", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		actor, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		gid := c.Params("id")
		target := c.Params("userId")
		if !isGroupMod(gid, actor) {
			return fiber.ErrForbidden
		}
		if _, err := opts.DB.Exec(`UPDATE study_group_members SET status='active' WHERE group_id=$1 AND user_id=$2`, gid, target); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// Delete a group message
	app.Delete("/v1/groups/messages/:msgId", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		actor, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		mid := c.Params("msgId")
		var gid string
		if err := opts.DB.Get(&gid, `SELECT group_id FROM study_group_messages WHERE id=$1`, mid); err != nil {
			return fiber.ErrNotFound
		}
		if !isGroupMod(gid, actor) {
			return fiber.ErrForbidden
		}
		if _, err := opts.DB.Exec(`DELETE FROM study_group_messages WHERE id=$1`, mid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// Delete a discussion post (class tutor/admin)
	app.Delete("/v1/discussions/posts/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		pid := c.Params("id")
		var cid string
		if err := opts.DB.Get(&cid, `SELECT d.class_id FROM class_posts p JOIN class_discussions d ON d.id=p.discussion_id WHERE p.id=$1`, pid); err != nil {
			return fiber.ErrNotFound
		}
		allowed, _ := auth.IsClassTutor(opts.DB, cid, uid)
		if !allowed {
			var sid *string
			_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, cid)
			if sid != nil && *sid != "" {
				allowed, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
			}
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		if _, err := opts.DB.Exec(`DELETE FROM class_posts WHERE id=$1`, pid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// Start a group call
	app.Post("/v1/groups/:id/call/start", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		gid := c.Params("id")
		var cid string
		if err := opts.DB.Get(&cid, `SELECT class_id FROM study_groups WHERE id=$1`, gid); err != nil {
			return fiber.ErrNotFound
		}
		if !isClassMember(cid, uid) {
			return fiber.ErrForbidden
		}
		code := strings.ToUpper(strings.ReplaceAll(uuid.New().String(), "-", ""))[:12]
		url := strings.TrimRight(providerBase(), "/") + "/" + code
		_, err = opts.DB.Exec(`INSERT INTO live_rooms (title, purpose, media, interaction, teacher_only, class_id, scheduled_at, room_code, provider_url)
			VALUES ($1,'study','video','interactive',false,$2, now(), $3, $4)`, "Study Group", cid, code, url)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": map[string]string{"url": url, "roomCode": code}})
	})

	// --- Breakout rooms ---
	app.Post("/v1/live/:id/breakouts", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		lid := c.Params("id")
		// verify permission: tutor or school admin for the class/school of the parent room
		var classID *string
		var schoolID *string
		_ = opts.DB.Get(&classID, `SELECT class_id FROM live_rooms WHERE id=$1`, lid)
		_ = opts.DB.Get(&schoolID, `SELECT school_id FROM live_rooms WHERE id=$1`, lid)
		allowed := false
		if classID != nil && *classID != "" {
			allowed, _ = auth.IsClassTutor(opts.DB, *classID, uid)
		}
		if !allowed && schoolID != nil && *schoolID != "" {
			allowed, _ = auth.IsSchoolAdmin(opts.DB, *schoolID, uid)
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct {
			Count int `json:"count"`
		}
		_ = c.BodyParser(&body)
		if body.Count <= 0 || body.Count > 20 {
			body.Count = 4
		}
		created := []fiber.Map{}
		for i := 1; i <= body.Count; i++ {
			code := strings.ToUpper(strings.ReplaceAll(uuid.New().String(), "-", ""))[:10]
			url := strings.TrimRight(providerBase(), "/") + "/" + code
			_, err = opts.DB.Exec(`INSERT INTO live_rooms (title, purpose, media, interaction, teacher_only, class_id, school_id, scheduled_at, room_code, provider_url, parent_room_id, breakout_index)
				VALUES ($1,'study','video','interactive',false, $2, $3, now(), $4, $5, $6, $7)`, "Breakout "+strconv.Itoa(i), nullIfEmptyStr(ptrString(classID)), nullIfEmptyStr(ptrString(schoolID)), code, url, lid, i)
			if err == nil {
				created = append(created, fiber.Map{"roomCode": code, "url": url, "index": i})
			}
		}
		return c.JSON(fiber.Map{"success": true, "data": created})
	})
	app.Get("/v1/live/:id/breakouts", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		lid := c.Params("id")
		var classID *string
		var schoolID *string
		_ = opts.DB.Get(&classID, `SELECT class_id FROM live_rooms WHERE id=$1`, lid)
		_ = opts.DB.Get(&schoolID, `SELECT school_id FROM live_rooms WHERE id=$1`, lid)
		member := false
		if classID != nil && *classID != "" {
			member = isClassMember(*classID, uid)
		}
		if !member && schoolID != nil && *schoolID != "" {
			_ = opts.DB.Get(&member, `SELECT EXISTS (SELECT 1 FROM school_members WHERE school_id=$1 AND user_id=$2 AND status='active')`, *schoolID, uid)
		}
		if !member {
			return fiber.ErrForbidden
		}
		var rows []struct {
			RoomCode, ProviderURL string
			Index                 *int
		}
		if err := opts.DB.Select(&rows, `SELECT room_code, provider_url, breakout_index FROM live_rooms WHERE parent_room_id=$1 ORDER BY breakout_index`, lid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// --- Appointments / Office Hours ---
	// List slots for a class (members only)
	app.Get("/v1/classes/:id/appointments/slots", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		cid := c.Params("id")
		if !isClassMember(cid, uid) {
			return fiber.ErrForbidden
		}
		type row struct {
			ID, TutorUserID string
			StartAt, EndAt  time.Time
			Capacity        int
			LocationURL     *string
			Notes           *string
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT id, tutor_user_id, start_at, end_at, capacity, location_url, notes FROM appointment_slots WHERE class_id=$1 AND end_at >= now() ORDER BY start_at ASC LIMIT 200`, cid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Create a slot (tutor or school admin)
	app.Post("/v1/classes/:id/appointments/slots", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		cid := c.Params("id")
		allowed, _ := auth.IsClassTutor(opts.DB, cid, uid)
		if !allowed {
			var sid *string
			_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, cid)
			if sid != nil && *sid != "" {
				allowed, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
			}
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct {
			StartAt     string  `json:"startAt"`
			EndAt       string  `json:"endAt"`
			Capacity    *int    `json:"capacity"`
			LocationURL *string `json:"locationUrl"`
			Notes       *string `json:"notes"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		if strings.TrimSpace(body.StartAt) == "" || strings.TrimSpace(body.EndAt) == "" {
			return fiber.ErrBadRequest
		}
		st, err1 := time.Parse(time.RFC3339, body.StartAt)
		en, err2 := time.Parse(time.RFC3339, body.EndAt)
		if err1 != nil || err2 != nil || !en.After(st) {
			return fiber.ErrBadRequest
		}
		cap := 1
		if body.Capacity != nil && *body.Capacity > 0 && *body.Capacity <= 50 {
			cap = *body.Capacity
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := opts.DB.Get(&out, `INSERT INTO appointment_slots (class_id, tutor_user_id, start_at, end_at, capacity, location_url, notes) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, cid, uid, st, en, cap, body.LocationURL, body.Notes); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})

	// Book a slot (student or guardian for child)
	app.Post("/v1/appointments/slots/:slotId/book", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("slotId")
		var row struct {
			ClassID        string
			StartAt, EndAt time.Time
			Capacity       int
		}
		if err := opts.DB.Get(&row, `SELECT class_id, start_at, end_at, capacity FROM appointment_slots WHERE id=$1`, sid); err != nil {
			return fiber.ErrNotFound
		}
		if !isClassMember(row.ClassID, uid) {
			return fiber.ErrForbidden
		}
		var body struct {
			ForUserID *string `json:"forUserId"`
		}
		_ = c.BodyParser(&body)
		// If guardian booking for child, verify linkage
		if body.ForUserID != nil && strings.TrimSpace(*body.ForUserID) != "" {
			var ok bool
			_ = opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM guardians_children WHERE guardian_user_id=$1 AND child_user_id=$2)`, uid, *body.ForUserID)
			if !ok {
				return fiber.ErrForbidden
			}
		}
		// Check capacity
		var count int
		_ = opts.DB.Get(&count, `SELECT COUNT(*) FROM appointment_bookings WHERE slot_id=$1 AND status='booked'`, sid)
		if count >= row.Capacity {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "Slot full"})
		}
		// Prevent duplicate
		var exists bool
		_ = opts.DB.Get(&exists, `SELECT EXISTS (SELECT 1 FROM appointment_bookings WHERE slot_id=$1 AND booker_user_id=$2 AND status='booked')`, sid, uid)
		if exists {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "Already booked"})
		}
		// Create booking
		var out struct {
			ID string `json:"id"`
		}
		var forUID any
		if body.ForUserID != nil {
			v := strings.TrimSpace(*body.ForUserID)
			if v != "" {
				forUID = v
			} else {
				forUID = nil
			}
		} else {
			forUID = nil
		}
		if err := opts.DB.Get(&out, `INSERT INTO appointment_bookings (slot_id, booker_user_id, for_user_id) VALUES ($1,$2,$3) RETURNING id`, sid, uid, forUID); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})

	// Cancel booking (booker or tutor)
	app.Post("/v1/appointments/bookings/:id/cancel", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		bid := c.Params("id")
		var rec struct {
			SlotID  string
			Booker  string
			ClassID string
		}
		if err := opts.DB.Get(&rec, `SELECT b.slot_id, b.booker_user_id, s.class_id FROM appointment_bookings b JOIN appointment_slots s ON s.id=b.slot_id WHERE b.id=$1`, bid); err != nil {
			return fiber.ErrNotFound
		}
		allowed := (rec.Booker == uid)
		if !allowed {
			allowed, _ = auth.IsClassTutor(opts.DB, rec.ClassID, uid)
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		if _, err := opts.DB.Exec(`UPDATE appointment_bookings SET status='cancelled', cancelled_at=now() WHERE id=$1 AND status='booked'`, bid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// List my bookings
	app.Get("/v1/appointments/my", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		type row struct {
			ID, SlotID, ClassID string
			StartAt, EndAt      time.Time
			Status              string
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT b.id, b.slot_id, s.class_id, s.start_at, s.end_at, b.status FROM appointment_bookings b JOIN appointment_slots s ON s.id=b.slot_id WHERE b.booker_user_id=$1 ORDER BY s.start_at DESC LIMIT 200`, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// ----- Collaborative resources integration -----

	// List resources by scope
	app.Get("/v1/resources", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		// Any authenticated user can list for scopes they belong to; simple filter here; deeper checks are enforced in UI flows
		if _, err := getUserID(c); err != nil {
			return fiber.ErrUnauthorized
		}
		scope := c.Query("scope")
		scopeID := c.Query("scope_id")
		if scope == "" || scopeID == "" {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "scope and scope_id required"})
		}
		type row struct {
			ID          string  `db:"id" json:"id"`
			Type        string  `db:"type" json:"type"`
			ExternalID  *string `db:"external_id" json:"externalId,omitempty"`
			URL         string  `db:"url" json:"url"`
			Title       *string `db:"title" json:"title,omitempty"`
			OwnerUserID string  `db:"owner_user_id" json:"ownerUserId"`
		}
		rows := []row{}
		q := `SELECT r.id, r.type, r.external_id, r.url, r.title, r.owner_user_id
              FROM resources r JOIN resource_links l ON l.resource_id=r.id
              WHERE l.scope=$1 AND l.scope_id=$2`
		if err := opts.DB.Select(&rows, q, scope, scopeID); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Create or link a resource and attach to a scope
	app.Post("/v1/resources", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var in struct {
			Mode       string  `json:"mode"` // create | link
			Type       string  `json:"type"`
			Title      *string `json:"title"`
			URL        *string `json:"url"`
			ExternalID *string `json:"externalId"`
			Scope      string  `json:"scope"`
			ScopeID    string  `json:"scopeId"`
		}
		if err := c.BodyParser(&in); err != nil {
			return c.Status(400).JSON(fiber.Map{"success": false})
		}
		mode := strings.ToLower(strings.TrimSpace(in.Mode))
		rtype := strings.ToLower(strings.TrimSpace(in.Type))
		if mode == "" {
			mode = "create"
		}
		if rtype == "" || in.Scope == "" || in.ScopeID == "" {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "type, scope, scopeId required"})
		}

		// Normalize allowed types
		allowed := map[string]bool{"docs": true, "sheets": true, "notes": true, "pdf": true, "slides": true}
		if !allowed[rtype] {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "invalid type"})
		}

		// Ensure or create resource row
		var res struct {
			ID, Type, URL, OwnerUserID string
			ExternalID                 *string
			Title                      *string
		}

		if mode == "link" {
			// Link an existing resource via URL or external ID
			if (in.URL == nil || strings.TrimSpace(*in.URL) == "") && (in.ExternalID == nil || strings.TrimSpace(*in.ExternalID) == "") {
				return c.Status(400).JSON(fiber.Map{"success": false, "message": "url or externalId required for link mode"})
			}
			urlStr := ""
			if in.URL != nil {
				urlStr = strings.TrimSpace(*in.URL)
			}
			ext := ""
			if in.ExternalID != nil {
				ext = strings.TrimSpace(*in.ExternalID)
			}
			// Upsert by (type, external_id) when given, else create by URL
			if ext != "" {
				// Try fetch existing
				err := opts.DB.Get(&res, `SELECT id, type, url, owner_user_id, external_id, title FROM resources WHERE type=$1 AND external_id=$2`, rtype, ext)
				if err != nil {
					// Create
					_ = opts.DB.Get(&res, `INSERT INTO resources (type, external_id, url, title, owner_user_id) VALUES ($1,$2,$3, NULLIF($4,''), $5)
                         RETURNING id, type, url, owner_user_id, external_id, title`, rtype, ext, urlStr, coalesceString(in.Title), uid)
				}
			} else {
				_ = opts.DB.Get(&res, `INSERT INTO resources (type, url, title, owner_user_id) VALUES ($1,$2,NULLIF($3,''),$4)
                      RETURNING id, type, url, owner_user_id, external_id, title`, rtype, urlStr, coalesceString(in.Title), uid)
			}
		} else {
			// Create new in editor service then record
			createdID := ""
			// Decide API endpoint per type
			appName := rtype
			api := editorAPIBase(appName, c.Hostname())
			var endpoint string
			switch rtype {
			case "docs":
				endpoint = strings.TrimRight(api, "/") + "/v1/docs"
			case "sheets":
				endpoint = strings.TrimRight(api, "/") + "/v1/sheets"
			case "notes":
				endpoint = strings.TrimRight(api, "/") + "/v1/notes"
			case "pdf":
				endpoint = strings.TrimRight(api, "/") + "/v1/pdfs"
			case "slides":
				endpoint = strings.TrimRight(api, "/") + "/v1/slides"
			}
			payload := map[string]any{}
			if in.Title != nil && strings.TrimSpace(*in.Title) != "" {
				switch rtype {
				case "docs":
					payload["title"] = *in.Title
				case "sheets":
					payload["title"] = *in.Title
				case "notes":
					payload["title"] = *in.Title
				case "pdf":
					payload["title"] = *in.Title
				case "slides":
					payload["title"] = *in.Title
				}
			}
			b, _ := json.Marshal(payload)
			req, _ := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			if v := c.Get("Cookie"); v != "" {
				req.Header.Set("Cookie", v)
			}
			httpClient := &http.Client{Timeout: 3 * time.Second}
			resp, err := httpClient.Do(req)
			if err != nil || resp.StatusCode >= 400 {
				if resp != nil {
					_ = resp.Body.Close()
				}
				return c.Status(502).JSON(fiber.Map{"success": false, "message": "create in editor failed"})
			}
			var out map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&out)
			_ = resp.Body.Close()
			data, _ := out["data"].(map[string]any)
			if data != nil {
				if s, ok := data["id"].(string); ok {
					createdID = s
				}
			}
			if createdID == "" {
				return c.Status(502).JSON(fiber.Map{"success": false, "message": "missing editor id"})
			}
			// Build frontend URL
			front := editorFrontendBase(appName, c.Hostname())
			var path string
			switch rtype {
			case "docs":
				path = "/editor/" + createdID
			case "sheets":
				path = "/sheet/" + createdID
			case "notes":
				path = "/note/" + createdID
			case "pdf":
				path = "/pdf/" + createdID
			case "slides":
				path = "/slide/" + createdID
			}
			fullURL := strings.TrimRight(front, "/") + path
			// Insert resource row
			if err := opts.DB.Get(&res, `INSERT INTO resources (type, external_id, url, title, owner_user_id)
               VALUES ($1,$2,$3,NULLIF($4,''),$5) RETURNING id, type, url, owner_user_id, external_id, title`, rtype, createdID, fullURL, coalesceString(in.Title), uid); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}

		// Attach to scope
		if _, err := opts.DB.Exec(`INSERT INTO resource_links (resource_id, scope, scope_id) VALUES ($1,$2,$3) ON CONFLICT (resource_id, scope, scope_id) DO NOTHING`, res.ID, in.Scope, in.ScopeID); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": res})
	})

	// Lightweight async queue for propagating ACL to editor services
	type aclTask struct{ Editor, Action, ExternalID, UserID, Role, Cookie, Host string }
	aclTasks := make(chan aclTask, 1024)
	go func() {
		client := &http.Client{Timeout: 2 * time.Second}
		for t := range aclTasks {
			api := editorAPIBase(t.Editor, t.Host)
			var endpoint string
			switch t.Editor {
			case "docs":
				endpoint = fmt.Sprintf("%s/v1/docs/%s/collaborators", strings.TrimRight(api, "/"), url.PathEscape(t.ExternalID))
			case "sheets":
				endpoint = fmt.Sprintf("%s/v1/sheets/%s/collaborators", strings.TrimRight(api, "/"), url.PathEscape(t.ExternalID))
			case "notes":
				endpoint = fmt.Sprintf("%s/v1/notes/%s/collaborators", strings.TrimRight(api, "/"), url.PathEscape(t.ExternalID))
			case "pdf":
				endpoint = fmt.Sprintf("%s/v1/pdfs/%s/collaborators", strings.TrimRight(api, "/"), url.PathEscape(t.ExternalID))
			case "slides":
				endpoint = fmt.Sprintf("%s/v1/slides/%s/collaborators", strings.TrimRight(api, "/"), url.PathEscape(t.ExternalID))
			default:
				continue
			}
			method := http.MethodPost
			var body io.Reader
			if t.Action == "revoke" {
				method = http.MethodDelete
			} else {
				payload := map[string]string{"userId": t.UserID, "role": t.Role}
				b, _ := json.Marshal(payload)
				body = bytes.NewReader(b)
			}
			req, _ := http.NewRequest(method, endpoint, body)
			if t.Action != "revoke" {
				req.Header.Set("Content-Type", "application/json")
			}
			if strings.TrimSpace(t.Cookie) != "" {
				req.Header.Set("Cookie", t.Cookie)
			}
			resp, err := client.Do(req)
			if err == nil && resp != nil {
				_ = resp.Body.Close()
			}
		}
	}()

	// Grant or update a role (principal can be user/class/group/school)
	app.Post("/v1/resources/:id/acl", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		actor, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		rid := c.Params("id")
		var body struct{ PrincipalType, PrincipalID, Role string }
		if err := c.BodyParser(&body); err != nil {
			return c.Status(400).JSON(fiber.Map{"success": false})
		}
		ptype := strings.ToLower(strings.TrimSpace(body.PrincipalType))
		role := strings.ToLower(strings.TrimSpace(body.Role))
		if ptype == "" || body.PrincipalID == "" || role == "" {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "principalType, principalId, role required"})
		}
		// Upsert
		if _, err := opts.DB.Exec(`INSERT INTO resource_acl (resource_id, principal_type, principal_id, role, created_by_user_id)
            VALUES ($1,$2,$3,$4,$5)
            ON CONFLICT (resource_id, principal_type, principal_id) DO UPDATE SET role=EXCLUDED.role, updated_at=now()`, rid, ptype, body.PrincipalID, role, actor); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		// Best-effort push to editor collaborators for user and non-user principals
		var r struct{ Type, ExternalID string }
		_ = opts.DB.Get(&r, `SELECT type, COALESCE(external_id,'') AS external_id FROM resources WHERE id=$1`, rid)
		if r.ExternalID != "" {
			host := c.Hostname()
			cookie := c.Get("Cookie")
			enqueue := func(userID string) {
				select {
				case aclTasks <- aclTask{Editor: r.Type, Action: "grant", ExternalID: r.ExternalID, UserID: userID, Role: role, Cookie: cookie, Host: host}:
				default:
				}
			}
			switch ptype {
			case "user":
				enqueue(body.PrincipalID)
			case "class":
				var tutor string
				_ = opts.DB.Get(&tutor, `SELECT tutor_user_id FROM classes WHERE id=$1`, body.PrincipalID)
				if tutor != "" {
					enqueue(tutor)
				}
				rows := []struct {
					UserID string `db:"user_id"`
				}{}
				_ = opts.DB.Select(&rows, `SELECT student_user_id AS user_id FROM class_enrollments WHERE class_id=$1 AND status='active'`, body.PrincipalID)
				for _, row := range rows {
					if row.UserID != "" {
						enqueue(row.UserID)
					}
				}
			case "group":
				rows := []struct {
					UserID string `db:"user_id"`
				}{}
				_ = opts.DB.Select(&rows, `SELECT user_id FROM study_group_members WHERE group_id=$1 AND status='active'`, body.PrincipalID)
				for _, row := range rows {
					if row.UserID != "" {
						enqueue(row.UserID)
					}
				}
			case "school":
				rows := []struct {
					UserID string `db:"user_id"`
				}{}
				_ = opts.DB.Select(&rows, `SELECT user_id FROM school_members WHERE school_id=$1 AND status='active'`, body.PrincipalID)
				for _, row := range rows {
					if row.UserID != "" {
						enqueue(row.UserID)
					}
				}
			}
		}
		return c.JSON(fiber.Map{"success": true})
	})

	app.Delete("/v1/resources/:id/acl", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		actor, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		_ = actor // audit-only; retain creator for trace
		rid := c.Params("id")
		ptype := strings.ToLower(strings.TrimSpace(c.Query("principal_type")))
		pid := strings.TrimSpace(c.Query("principal_id"))
		if ptype == "" || pid == "" {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "principal_type and principal_id required"})
		}
		if _, err := opts.DB.Exec(`DELETE FROM resource_acl WHERE resource_id=$1 AND principal_type=$2 AND principal_id=$3`, rid, ptype, pid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		// Best-effort editor revoke for user and non-user principals
		var r struct{ Type, ExternalID string }
		_ = opts.DB.Get(&r, `SELECT type, COALESCE(external_id,'') AS external_id FROM resources WHERE id=$1`, rid)
		if r.ExternalID != "" {
			host := c.Hostname()
			cookie := c.Get("Cookie")
			enqueue := func(userID string) {
				select {
				case aclTasks <- aclTask{Editor: r.Type, Action: "revoke", ExternalID: r.ExternalID, UserID: userID, Cookie: cookie, Host: host}:
				default:
				}
			}
			switch ptype {
			case "user":
				enqueue(pid)
			case "class":
				// revoke tutor + all students (current snapshot)
				var tutor string
				_ = opts.DB.Get(&tutor, `SELECT tutor_user_id FROM classes WHERE id=$1`, pid)
				if tutor != "" {
					enqueue(tutor)
				}
				rows := []struct {
					UserID string `db:"user_id"`
				}{}
				_ = opts.DB.Select(&rows, `SELECT student_user_id AS user_id FROM class_enrollments WHERE class_id=$1`, pid)
				for _, row := range rows {
					if row.UserID != "" {
						enqueue(row.UserID)
					}
				}
			case "group":
				rows := []struct {
					UserID string `db:"user_id"`
				}{}
				_ = opts.DB.Select(&rows, `SELECT user_id FROM study_group_members WHERE group_id=$1`, pid)
				for _, row := range rows {
					if row.UserID != "" {
						enqueue(row.UserID)
					}
				}
			case "school":
				rows := []struct {
					UserID string `db:"user_id"`
				}{}
				_ = opts.DB.Select(&rows, `SELECT user_id FROM school_members WHERE school_id=$1`, pid)
				for _, row := range rows {
					if row.UserID != "" {
						enqueue(row.UserID)
					}
				}
			}
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// List ACL entries for a resource
	app.Get("/v1/resources/:id/acl", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		if _, err := getUserID(c); err != nil {
			return fiber.ErrUnauthorized
		}
		rid := c.Params("id")
		type row struct {
			ID            string    `db:"id" json:"id"`
			PrincipalType string    `db:"principal_type" json:"principalType"`
			PrincipalID   string    `db:"principal_id" json:"principalId"`
			Role          string    `db:"role" json:"role"`
			CreatedBy     string    `db:"created_by_user_id" json:"createdBy"`
			CreatedAt     time.Time `db:"created_at" json:"createdAt"`
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT id, principal_type, principal_id, role, created_by_user_id, created_at FROM resource_acl WHERE resource_id=$1 ORDER BY created_at DESC`, rid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Attach/detach link to another scope
	app.Post("/v1/resources/:id/links", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		if _, err := getUserID(c); err != nil {
			return fiber.ErrUnauthorized
		}
		rid := c.Params("id")
		var body struct {
			Scope, ScopeID string
			DefaultRole    *string
		}
		if err := c.BodyParser(&body); err != nil {
			return c.Status(400).JSON(fiber.Map{"success": false})
		}
		if body.Scope == "" || body.ScopeID == "" {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "scope and scopeId required"})
		}
		var dRole any
		if body.DefaultRole != nil && strings.TrimSpace(*body.DefaultRole) != "" {
			dRole = strings.ToLower(strings.TrimSpace(*body.DefaultRole))
		} else {
			dRole = nil
		}
		if _, err := opts.DB.Exec(`INSERT INTO resource_links (resource_id, scope, scope_id, default_role) VALUES ($1,$2,$3,$4) ON CONFLICT (resource_id, scope, scope_id) DO UPDATE SET default_role=EXCLUDED.default_role, created_at=resource_links.created_at`, rid, body.Scope, body.ScopeID, dRole); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Delete("/v1/resources/:id/links", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		if _, err := getUserID(c); err != nil {
			return fiber.ErrUnauthorized
		}
		rid := c.Params("id")
		scope := c.Query("scope")
		scopeID := c.Query("scope_id")
		if scope == "" || scopeID == "" {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "scope and scope_id required"})
		}
		if _, err := opts.DB.Exec(`DELETE FROM resource_links WHERE resource_id=$1 AND scope=$2 AND scope_id=$3`, rid, scope, scopeID); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// Open a resource: 302 to SSO redirect pointing at editor URL
	app.Get("/v1/resources/:id/open", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		if _, err := getUserID(c); err != nil {
			return fiber.ErrUnauthorized
		}
		rid := c.Params("id")
		var r struct{ Type, URL string }
		if err := opts.DB.Get(&r, `SELECT type, url FROM resources WHERE id=$1`, rid); err != nil {
			return fiber.ErrNotFound
		}
		// only allow editor hosts
		u := strings.TrimSpace(r.URL)
		redir := fmt.Sprintf("/v1/sso/redirect?to=%s", url.QueryEscape(u))
		return c.Redirect(redir, http.StatusFound)
	})

	// Resource history: return editor history URL (link-out) and open URL
	app.Get("/v1/resources/:id/history", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		if _, err := getUserID(c); err != nil {
			return fiber.ErrUnauthorized
		}
		rid := c.Params("id")
		var r struct{ Type, URL string }
		if err := opts.DB.Get(&r, `SELECT type, url FROM resources WHERE id=$1`, rid); err != nil {
			return fiber.ErrNotFound
		}
		u := strings.TrimSpace(r.URL)
		openURL := fmt.Sprintf("/v1/sso/redirect?to=%s", url.QueryEscape(u))
		// Heuristic: most editors support a history view. Prefer query param; fallback to anchor.
		historyRaw := u
		if strings.Contains(u, "?") {
			historyRaw = u + "&history=1"
		} else {
			historyRaw = u + "?history=1"
		}
		historyURL := fmt.Sprintf("/v1/sso/redirect?to=%s", url.QueryEscape(historyRaw))
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"open_url": openURL, "history_url": historyURL}})
	})

	// Minimal SSO redirect: verifies Core API session and forwards to same-root editor URL
	app.Get("/v1/sso/redirect", func(c *fiber.Ctx) error {
		to := c.Query("to")
		if strings.TrimSpace(to) == "" {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "to required"})
		}
		// Verify session via Core API
		req, _ := http.NewRequest("POST", strings.TrimRight(opts.CoreAPIBase, "/")+"/v1/auth/verify", nil)
		if v := c.Get("Cookie"); v != "" {
			req.Header.Set("Cookie", v)
		}
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		defer resp.Body.Close()
		var raw map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&raw)
		data, _ := raw["data"].(map[string]any)
		valid := false
		if data != nil {
			valid, _ = data["valid"].(bool)
		}
		if !valid {
			return fiber.ErrUnauthorized
		}
		// Allow only berjis.* editors
		p, err := url.Parse(to)
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "invalid to"})
		}
		host := strings.ToLower(p.Hostname())
		if !(strings.HasSuffix(host, ".berjis.tech") || strings.EqualFold(host, "berjis.tech") || strings.HasSuffix(host, ".berjis.com") || strings.EqualFold(host, "berjis.com") || strings.HasSuffix(host, ".berjis.test") || strings.EqualFold(host, "berjis.test")) {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "cross-domain not allowed"})
		}
		return c.Redirect(to, http.StatusFound)
	})

	// ===== Additional AI Utilities =====
	type aiReq struct {
		Text    string `json:"text"`
		Lang    string `json:"lang"`
		Target  string `json:"target"`
		Context string `json:"context"`
	}
	callAI := func(c *fiber.Ctx, sys string, user string, lang string) error {
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if opts.DB != nil {
			var enabled bool = true
			_ = opts.DB.Get(&enabled, `SELECT ai_enabled FROM ai_settings WHERE user_id=$1`, uid)
			if !enabled {
				return c.Status(403).JSON(fiber.Map{"success": false, "message": "AI access is disabled for this account."})
			}
		}
		body := map[string]any{"model": "gpt-4o-mini", "lang": lang, "messages": []map[string]string{{"role": "system", "content": sys}, {"role": "user", "content": user}}}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", c.BaseURL()+"/v1/ai/chat", bytes.NewReader(b))
		if v := c.Get("Authorization"); v != "" {
			req.Header.Set("Authorization", v)
		}
		if v := c.Get("Cookie"); v != "" {
			req.Header.Set("Cookie", v)
		}
		req.Header.Set("Content-Type", "application/json")
		if lang != "" {
			req.Header.Set("Accept-Language", lang)
		}
		cli := &http.Client{Timeout: 30 * time.Second}
		resp, err := cli.Do(req)
		if err != nil {
			return c.Status(502).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		defer resp.Body.Close()
		c.Set("Content-Type", resp.Header.Get("Content-Type"))
		c.Status(resp.StatusCode)
		_, _ = io.Copy(c, resp.Body)
		return nil
	}
	app.Post("/v1/ai/summarize", func(c *fiber.Ctx) error {
		var b aiReq
		_ = c.BodyParser(&b)
		if strings.TrimSpace(b.Text) == "" {
			return fiber.ErrBadRequest
		}
		sys := "You are an educational summarizer. Produce a concise summary with bullet points for a student."
		return callAI(c, sys, b.Text, b.Lang)
	})
	app.Post("/v1/ai/translate", func(c *fiber.Ctx) error {
		var b aiReq
		_ = c.BodyParser(&b)
		if strings.TrimSpace(b.Text) == "" || strings.TrimSpace(b.Target) == "" {
			return fiber.ErrBadRequest
		}
		sys := fmt.Sprintf("Translate to %s preserving school terminology.", b.Target)
		return callAI(c, sys, b.Text, b.Lang)
	})
	app.Post("/v1/ai/practice", func(c *fiber.Ctx) error {
		var b aiReq
		_ = c.BodyParser(&b)
		if strings.TrimSpace(b.Text) == "" {
			return fiber.ErrBadRequest
		}
		sys := "Generate 5 practice questions (mix of MCQ and short answer) with suggested answers based on the lesson text. Return compact text."
		return callAI(c, sys, b.Text, b.Lang)
	})
	app.Post("/v1/ai/path", func(c *fiber.Ctx) error {
		var b aiReq
		_ = c.BodyParser(&b)
		if strings.TrimSpace(b.Text) == "" {
			return fiber.ErrBadRequest
		}
		sys := "Given a student profile and progress, suggest a short personalized learning path with 3-5 steps and brief rationales."
		return callAI(c, sys, b.Text, b.Lang)
	})
	app.Post("/v1/ai/tts", func(c *fiber.Ctx) error {
		var b aiReq
		_ = c.BodyParser(&b)
		if strings.TrimSpace(b.Text) == "" {
			return fiber.ErrBadRequest
		}
		sys := "Convert the text to a SSML-like plain text suitable for TTS engines."
		return callAI(c, sys, b.Text, b.Lang)
	})

	// Homework help (integrity-safe): provide hints/steps, no final answers
	app.Post("/v1/ai/homework-help", func(c *fiber.Ctx) error {
		var b struct {
			Question string `json:"question"`
			Subject  string `json:"subject"`
		}
		if err := c.BodyParser(&b); err != nil || strings.TrimSpace(b.Question) == "" {
			return fiber.ErrBadRequest
		}
		// guard: block explicit solution requests
		q := strings.ToLower(b.Question)
		blocked := []string{"give me the full answer", "solve it fully", "final answer", "exact answer", "write my assignment", "do my homework"}
		for _, w := range blocked {
			if strings.Contains(q, w) {
				return c.Status(400).JSON(fiber.Map{"success": false, "message": "We can guide you with hints and steps, not final answers."})
			}
		}
		sub := strings.TrimSpace(b.Subject)
		sys := "You are a tutor that preserves academic integrity. Provide step-by-step guidance, key concepts, and small hints. Do NOT give final numeric values or full essays. Ask brief check questions."
		prompt := fmt.Sprintf("Subject: %s\nProblem: %s\nInstructions: Provide 3-6 steps, 2-3 hints, and 1 check-your-understanding question. Avoid final answers.", sub, b.Question)
		return callAI(c, sys, prompt, "")
	})

	// Study material generation (outline, key points, flashcards)
	app.Post("/v1/ai/study-materials", func(c *fiber.Ctx) error {
		var b struct {
			Text  string `json:"text"`
			Level string `json:"level"`
		}
		if err := c.BodyParser(&b); err != nil || strings.TrimSpace(b.Text) == "" {
			return fiber.ErrBadRequest
		}
		level := strings.TrimSpace(strings.ToLower(b.Level))
		if level == "" {
			level = "general"
		}
		sys := "You create concise study packs (outline, key points, and 10 flashcards as Q/A) for students. Keep language clear and role-appropriate."
		payload := fmt.Sprintf("Level: %s\nSource:\n%s\n\nReturn sections: OUTLINE, KEY POINTS (bullets), FLASHCARDS (Q: / A:).", level, b.Text)
		return callAI(c, sys, payload, "")
	})

	// ===== Adaptive Testing =====
	app.Post("/v1/tests/:id/start-adaptive", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		tid := c.Params("id")
		var mode string
		if err := opts.DB.Get(&mode, `SELECT mode FROM tests WHERE id=$1`, tid); err != nil || mode != "adaptive" {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`INSERT INTO adaptive_attempts (test_id, student_user_id) VALUES ($1,$2)
            ON CONFLICT (test_id, student_user_id) DO UPDATE SET status='in_progress', current_index=0, asked_question_ids='{}'::uuid[], current_difficulty=3, updated_at=now()`, tid, uid)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		var id string
		_ = opts.DB.Get(&id, `SELECT id FROM adaptive_attempts WHERE test_id=$1 AND student_user_id=$2`, tid, uid)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"attemptId": id}})
	})
	app.Get("/v1/adaptive/:attemptId/next", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		aid := c.Params("attemptId")
		var row struct {
			TestID string   `db:"test_id"`
			Asked  []string `db:"asked_question_ids"`
			Cur    int      `db:"current_difficulty"`
			Index  int      `db:"current_index"`
			Total  int      `db:"total_questions"`
		}
		if err := opts.DB.Get(&row, `SELECT test_id, array(SELECT x::text FROM unnest(asked_question_ids) x) AS asked_question_ids, current_difficulty, current_index, total_questions FROM adaptive_attempts WHERE id=$1 AND student_user_id=$2 AND status='in_progress'`, aid, uid); err != nil {
			return fiber.ErrNotFound
		}
		// select next question matching difficulty not asked yet
		var q struct {
			ID      string `db:"id"`
			Prompt  string `db:"prompt"`
			QType   string `db:"qtype"`
			Options []byte `db:"options"`
			Points  int    `db:"points"`
		}
		arr := uuidArrayLiteral(row.Asked)
		if err := opts.DB.Get(&q, `SELECT id, prompt, qtype, COALESCE(options,'null'::jsonb) AS options, points FROM test_questions 
            WHERE test_id=$1 AND difficulty=$2 AND id <> ALL($3::uuid[]) ORDER BY random() LIMIT 1`, row.TestID, row.Cur, arr); err != nil {
			if err := opts.DB.Get(&q, `SELECT id, prompt, qtype, COALESCE(options,'null'::jsonb) AS options, points FROM test_questions 
                WHERE test_id=$1 AND id <> ALL($2::uuid[]) ORDER BY difficulty, order_index LIMIT 1`, row.TestID, arr); err != nil {
				return c.JSON(fiber.Map{"success": true, "data": nil})
			}
		}
		return c.JSON(fiber.Map{"success": true, "data": q})
	})
	app.Post("/v1/adaptive/:attemptId/answer", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		aid := c.Params("attemptId")
		var b struct {
			QuestionID string `json:"questionId"`
			Correct    *bool  `json:"correct"`
		}
		if err := c.BodyParser(&b); err != nil || strings.TrimSpace(b.QuestionID) == "" || b.Correct == nil {
			return fiber.ErrBadRequest
		}
		var st struct {
			Asked []string `db:"asked_question_ids"`
			Cur   int      `db:"current_difficulty"`
			Index int      `db:"current_index"`
			Total int      `db:"total_questions"`
			Theta *float64 `db:"theta"`
		}
		if err := opts.DB.Get(&st, `SELECT array(SELECT x::text FROM unnest(asked_question_ids) x) AS asked_question_ids, current_difficulty, current_index, total_questions, theta FROM adaptive_attempts WHERE id=$1 AND student_user_id=$2 AND status='in_progress'`, aid, uid); err != nil {
			return fiber.ErrNotFound
		}
		asked := append(st.Asked, b.QuestionID)
		idx := st.Index + 1
		th := 0.0
		if st.Theta != nil {
			th = *st.Theta
		}
		if *b.Correct {
			th += 0.5
		} else {
			th -= 0.5
		}
		if th > 3 {
			th = 3
		}
		if th < -3 {
			th = -3
		}
		nd := 3
		if th <= -1.5 {
			nd = 1
		} else if th <= -0.5 {
			nd = 2
		} else if th >= 1.5 {
			nd = 5
		} else if th >= 0.5 {
			nd = 4
		} else {
			nd = 3
		}
		_, err = opts.DB.Exec(`UPDATE adaptive_attempts SET asked_question_ids=$1::uuid[], current_index=$2, current_difficulty=$3, theta=$4, updated_at=now() WHERE id=$5`, uuidArrayLiteral(asked), idx, nd, th, aid)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"nextDifficulty": nd, "index": idx, "total": st.Total}})
	})
	app.Post("/v1/adaptive/:attemptId/finish", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		aid := c.Params("attemptId")
		var th float64
		if err := opts.DB.Get(&th, `SELECT COALESCE(theta,0) FROM adaptive_attempts WHERE id=$1 AND student_user_id=$2`, aid, uid); err != nil {
			return fiber.ErrNotFound
		}
		score := 50.0 + (th/3.0)*50.0
		if score < 0 {
			score = 0
		}
		if score > 100 {
			score = 100
		}
		_, _ = opts.DB.Exec(`UPDATE adaptive_attempts SET status='completed', score=$1, updated_at=now() WHERE id=$2`, score, aid)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"score": score}})
	})

	// ===== Progress Insights =====
	app.Get("/v1/classes/:id/insights", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		cid := c.Params("id")
		allowed, _ := auth.IsClassTutor(opts.DB, cid, uid)
		if !allowed {
			var sid *string
			_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, cid)
			if sid != nil {
				allowed, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
			}
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var students int
		_ = opts.DB.Get(&students, `SELECT COUNT(*) FROM class_enrollments WHERE class_id=$1 AND status='active'`, cid)
		var lessons int
		_ = opts.DB.Get(&lessons, `SELECT COUNT(*) FROM lessons l JOIN subjects s ON s.id=l.subject_id WHERE s.class_id=$1 AND l.status='active'`, cid)
		var completed int
		_ = opts.DB.Get(&completed, `SELECT COUNT(*) FROM lesson_progress lp WHERE lp.lesson_id IN (SELECT l.id FROM lessons l JOIN subjects s ON s.id=l.subject_id WHERE s.class_id=$1) AND lp.status='completed'`, cid)
		var tests int
		_ = opts.DB.Get(&tests, `SELECT COUNT(*) FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted'`, cid)
		var attempts int
		_ = opts.DB.Get(&attempts, `SELECT COUNT(*) FROM test_attempts ta WHERE ta.test_id IN (SELECT t.id FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted') AND ta.status<>'in_progress'`, cid)
		var avgPct *float64
		_ = opts.DB.Get(&avgPct, `SELECT NULLIF(AVG(score),0) FROM test_attempts ta WHERE ta.status<>'in_progress' AND ta.test_id IN (SELECT t.id FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted')`, cid)
		var inactive int
		_ = opts.DB.Get(&inactive, `SELECT COUNT(*) FROM class_enrollments ce WHERE ce.class_id=$1 AND NOT EXISTS (SELECT 1 FROM lesson_progress lp WHERE lp.student_user_id=ce.student_user_id AND lp.lesson_id IN (SELECT l.id FROM lessons l JOIN subjects s ON s.id=l.subject_id WHERE s.class_id=$1) AND lp.updated_at> now()- interval '14 days')`, cid)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"students": students, "lessons": lessons, "lessonCompletions": completed, "tests": tests, "testAttempts": attempts, "averagePercent": avgPct, "inactive14d": inactive}})
	})
	app.Post("/v1/classes/:id/insights/suggest", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		cid := c.Params("id")
		allowed, _ := auth.IsClassTutor(opts.DB, cid, uid)
		if !allowed {
			var sid *string
			_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, cid)
			if sid != nil {
				allowed, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
			}
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var metrics map[string]any
		if err := opts.DB.Get(&metrics, `SELECT json_build_object(
			'students', (SELECT COUNT(*) FROM class_enrollments WHERE class_id=$1 AND status='active'),
			'lessons', (SELECT COUNT(*) FROM lessons l JOIN subjects s ON s.id=l.subject_id WHERE s.class_id=$1 AND l.status='active'),
			'lessonCompletions', (SELECT COUNT(*) FROM lesson_progress lp WHERE lp.lesson_id IN (SELECT l.id FROM lessons l JOIN subjects s ON s.id=l.subject_id WHERE s.class_id=$1) AND lp.status='completed'),
			'tests', (SELECT COUNT(*) FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted'),
			'testAttempts', (SELECT COUNT(*) FROM test_attempts ta WHERE ta.test_id IN (SELECT t.id FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted') AND ta.status<>'in_progress'),
			'averagePercent', (SELECT NULLIF(AVG(score),0) FROM test_attempts ta WHERE ta.status<>'in_progress' AND ta.test_id IN (SELECT t.id FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted')),
			'inactive14d', (SELECT COUNT(*) FROM class_enrollments ce WHERE ce.class_id=$1 AND NOT EXISTS (SELECT 1 FROM lesson_progress lp WHERE lp.student_user_id=ce.student_user_id AND lp.lesson_id IN (SELECT l.id FROM lessons l JOIN subjects s ON s.id=l.subject_id WHERE s.class_id=$1) AND lp.updated_at> now()- interval '14 days'))
		)`, cid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		b, _ := json.Marshal(metrics)
		sys := "You are an academic advisor. Given class metrics JSON, produce short, actionable insights and next-step suggestions for the teacher (3-6 bullets), including at-risk and high-performing notes."
		return callAI(c, sys, string(b), "")
	})

	// ===== Grade Levels =====
	app.Get("/v1/schools/:id/grade-levels", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		sid := c.Params("id")
		var rows []map[string]any
		if err := opts.DB.Select(&rows, `SELECT id, code, name, order_index, min_age, max_age FROM grade_levels WHERE school_id=$1 ORDER BY order_index, name`, sid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/schools/:id/grade-levels", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		var isAdmin bool
		_ = opts.DB.Get(&isAdmin, `SELECT EXISTS (SELECT 1 FROM school_members WHERE school_id=$1 AND user_id=$2 AND role='admin' AND status='active')`, sid, uid)
		if !isAdmin {
			return fiber.ErrForbidden
		}
		var b struct {
			Code, Name string
			OrderIndex *int
			MinAge     *int
			MaxAge     *int
		}
		if err := c.BodyParser(&b); err != nil || strings.TrimSpace(b.Code) == "" || strings.TrimSpace(b.Name) == "" {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`INSERT INTO grade_levels (school_id, code, name, order_index, min_age, max_age) VALUES ($1,$2,$3,COALESCE($4,0),$5,$6)`, sid, b.Code, b.Name, b.OrderIndex, b.MinAge, b.MaxAge)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Patch("/v1/grade-levels/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		id := c.Params("id")
		var sid string
		if err := opts.DB.Get(&sid, `SELECT school_id FROM grade_levels WHERE id=$1`, id); err != nil {
			return fiber.ErrNotFound
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var isAdmin bool
		_ = opts.DB.Get(&isAdmin, `SELECT EXISTS (SELECT 1 FROM school_members WHERE school_id=$1 AND user_id=$2 AND role='admin' AND status='active')`, sid, uid)
		if !isAdmin {
			return fiber.ErrForbidden
		}
		var b struct {
			Code, Name *string
			OrderIndex *int
			MinAge     *int
			MaxAge     *int
		}
		_ = c.BodyParser(&b)
		_, err = opts.DB.Exec(`UPDATE grade_levels SET code=COALESCE($1,code), name=COALESCE($2,name), order_index=COALESCE($3,order_index), min_age=COALESCE($4,min_age), max_age=COALESCE($5,max_age) WHERE id=$6`, b.Code, b.Name, b.OrderIndex, b.MinAge, b.MaxAge, id)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// ===== Curriculum Frameworks =====
	app.Post("/v1/schools/:id/frameworks", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		sid := c.Params("id")
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var isAdmin bool
		_ = opts.DB.Get(&isAdmin, `SELECT EXISTS (SELECT 1 FROM school_members WHERE school_id=$1 AND user_id=$2 AND role='admin' AND status='active')`, sid, uid)
		if !isAdmin {
			return fiber.ErrForbidden
		}
		var b struct{ Region, Name, Description string }
		if err := c.BodyParser(&b); err != nil || strings.TrimSpace(b.Name) == "" {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`INSERT INTO curriculum_frameworks (school_id, region, name, description, created_by_user_id) VALUES ($1,$2,$3,$4,$5)`, sid, nullIfEmptyStr(b.Region), b.Name, nullIfEmptyStr(b.Description), uid)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Get("/v1/schools/:id/frameworks", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		sid := c.Params("id")
		var rows []map[string]any
		if err := opts.DB.Select(&rows, `SELECT id, region, name, description, created_at FROM curriculum_frameworks WHERE school_id=$1 OR school_id IS NULL ORDER BY created_at DESC`, sid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/frameworks/:id/outcomes", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		fid := c.Params("id")
		var b struct {
			Code, Description, GradeBand string
			OrderIndex                   *int
		}
		if err := c.BodyParser(&b); err != nil || strings.TrimSpace(b.Description) == "" {
			return fiber.ErrBadRequest
		}
		_, err := opts.DB.Exec(`INSERT INTO learning_outcomes (framework_id, code, description, grade_band, order_index) VALUES ($1,$2,$3,NULLIF($4,''),COALESCE($5,0))`, fid, nullIfEmptyStr(b.Code), b.Description, b.GradeBand, b.OrderIndex)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Post("/v1/lessons/:id/outcomes", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		lid := c.Params("id")
		var b struct{ OutcomeID string }
		if err := c.BodyParser(&b); err != nil || strings.TrimSpace(b.OutcomeID) == "" {
			return fiber.ErrBadRequest
		}
		_, err := opts.DB.Exec(`INSERT INTO lesson_outcomes (lesson_id, outcome_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, lid, b.OutcomeID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Post("/v1/tests/:id/outcomes", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		tid := c.Params("id")
		var b struct{ OutcomeID string }
		if err := c.BodyParser(&b); err != nil || strings.TrimSpace(b.OutcomeID) == "" {
			return fiber.ErrBadRequest
		}
		_, err := opts.DB.Exec(`INSERT INTO test_outcomes (test_id, outcome_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, tid, b.OutcomeID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// ===== Accreditations =====
	app.Get("/v1/schools/:id/accreditations", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		sid := c.Params("id")
		var rows []map[string]any
		if err := opts.DB.Select(&rows, `SELECT id, region, accreditor_name, accreditation_id, status, issued_at, expires_at, evidence_url FROM school_accreditations WHERE school_id=$1 ORDER BY created_at DESC`, sid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/schools/:id/accreditations", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		var isAdmin bool
		_ = opts.DB.Get(&isAdmin, `SELECT EXISTS (SELECT 1 FROM school_members WHERE school_id=$1 AND user_id=$2 AND role='admin' AND status='active')`, sid, uid)
		if !isAdmin {
			return fiber.ErrForbidden
		}
		var b struct{ Region, AccreditorName, AccreditationID, Status, EvidenceURL, IssuedAt, ExpiresAt string }
		if err := c.BodyParser(&b); err != nil || strings.TrimSpace(b.Region) == "" || strings.TrimSpace(b.AccreditorName) == "" {
			return fiber.ErrBadRequest
		}
		_, err = opts.DB.Exec(`INSERT INTO school_accreditations (school_id, region, accreditor_name, accreditation_id, status, evidence_url, issued_at, expires_at) VALUES ($1,$2,$3,NULLIF($4,''),COALESCE(NULLIF($5,''),'active'), NULLIF($6,''), NULLIF($7,'')::timestamptz, NULLIF($8,'')::timestamptz)`, sid, b.Region, b.AccreditorName, b.AccreditationID, b.Status, b.EvidenceURL, b.IssuedAt, b.ExpiresAt)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// ===== Legal & Consents =====
	app.Get("/v1/legal/documents", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		region := strings.TrimSpace(c.Query("region"))
		if region == "" {
			region = "default"
		}
		var rows []map[string]any
		if err := opts.DB.Select(&rows, `SELECT id, region, kind, version, title, url, effective_at FROM legal_documents WHERE region IN ($1,'default') ORDER BY effective_at DESC`, region); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/legal/consent", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var b struct{ Region, Kind, Version string }
		if err := c.BodyParser(&b); err != nil || strings.TrimSpace(b.Kind) == "" || strings.TrimSpace(b.Version) == "" {
			return fiber.ErrBadRequest
		}
		reg := strings.TrimSpace(b.Region)
		if reg == "" {
			reg = "default"
		}
		_, err = opts.DB.Exec(`INSERT INTO user_consents (user_id, region, kind, version) VALUES ($1,$2,$3,$4)
            ON CONFLICT (user_id, region, kind) DO UPDATE SET version=EXCLUDED.version, consented_at=now()`, uid, reg, strings.ToLower(b.Kind), b.Version)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Post("/v1/legal/parental-consent/request", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var b struct{ ChildUserID, GuardianUserID string }
		if err := c.BodyParser(&b); err != nil || strings.TrimSpace(b.ChildUserID) == "" || strings.TrimSpace(b.GuardianUserID) == "" {
			return fiber.ErrBadRequest
		}
		var ok bool
		_ = opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM guardians_children WHERE guardian_user_id=$1 AND child_user_id=$2)`, b.GuardianUserID, b.ChildUserID)
		if !ok {
			return fiber.ErrForbidden
		}
		if uid != b.GuardianUserID {
			var count int
			_ = opts.DB.Get(&count, `SELECT COUNT(*) FROM school_members m WHERE m.user_id=$1 AND m.role='admin' AND EXISTS (SELECT 1 FROM school_members m2 WHERE m2.user_id=$2 AND m2.school_id=m.school_id)`, uid, b.ChildUserID)
			if count == 0 {
				return fiber.ErrForbidden
			}
		}
		_, err = opts.DB.Exec(`INSERT INTO parental_consents (child_user_id, guardian_user_id) VALUES ($1,$2) ON CONFLICT (child_user_id, guardian_user_id) DO UPDATE SET status='pending', requested_at=now(), responded_at=NULL`, b.ChildUserID, b.GuardianUserID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Post("/v1/legal/parental-consent/respond", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var b struct {
			ChildUserID string
			Decision    string
			Notes       string
		}
		if err := c.BodyParser(&b); err != nil || strings.TrimSpace(b.ChildUserID) == "" {
			return fiber.ErrBadRequest
		}
		dec := strings.ToLower(strings.TrimSpace(b.Decision))
		switch dec {
		case "approved", "denied", "revoked":
		default:
			dec = "approved"
		}
		var ok bool
		_ = opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM guardians_children WHERE guardian_user_id=$1 AND child_user_id=$2)`, uid, b.ChildUserID)
		if !ok {
			return fiber.ErrForbidden
		}
		_, err = opts.DB.Exec(`UPDATE parental_consents SET status=$1, notes=NULLIF($2,''), responded_at=now() WHERE child_user_id=$3 AND guardian_user_id=$4`, dec, b.Notes, b.ChildUserID, uid)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	return app
}

// ----- Resources + ACL + SSO redirect -----

// deriveRootDomain converts a host like schools.berjis.tech to berjis.tech
func deriveRootDomain(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return "berjis.tech"
	}
	parts := strings.Split(h, ".")
	if len(parts) < 2 {
		return h
	}
	// Keep last two labels by default
	base := strings.Join(parts[len(parts)-2:], ".")
	if strings.HasSuffix(base, "berjis.tech") || strings.HasSuffix(base, "berjis.com") || strings.HasSuffix(base, "berjis.test") {
		return base
	}
	// fallback
	return "berjis.tech"
}

func editorFrontendBase(app, host string) string {
	root := deriveRootDomain(host)
	if root == "" {
		root = "berjis.tech"
	}
	return fmt.Sprintf("https://%s.%s", app, root)
}
func editorAPIBase(app, host string) string {
	root := deriveRootDomain(host)
	if root == "" {
		root = "berjis.tech"
	}
	return fmt.Sprintf("https://%s-api.%s", app, root)
}

// propagateACL best-effort calls into editor APIs when present; failures are tolerated.
func propagateACL(c *fiber.Ctx, coreBase string, editor string, action string, externalID string, targetUser string, role string) {
	// Map editor to API and endpoint pattern (best guess per services present)
	api := editorAPIBase(editor, c.Hostname())
	var endpoint string
	switch editor {
	case "docs":
		endpoint = fmt.Sprintf("%s/v1/docs/%s/collaborators", strings.TrimRight(api, "/"), url.PathEscape(externalID))
	case "sheets":
		endpoint = fmt.Sprintf("%s/v1/sheets/%s/collaborators", strings.TrimRight(api, "/"), url.PathEscape(externalID))
	case "notes":
		endpoint = fmt.Sprintf("%s/v1/notes/%s/collaborators", strings.TrimRight(api, "/"), url.PathEscape(externalID))
	case "pdf":
		endpoint = fmt.Sprintf("%s/v1/pdfs/%s/collaborators", strings.TrimRight(api, "/"), url.PathEscape(externalID))
	case "slides":
		endpoint = fmt.Sprintf("%s/v1/slides/%s/collaborators", strings.TrimRight(api, "/"), url.PathEscape(externalID))
	default:
		return
	}
	method := http.MethodPost
	if action == "revoke" {
		method = http.MethodDelete
	}
	payload := map[string]any{"userId": targetUser, "role": role}
	if action == "revoke" {
		payload = nil
	}
	var body io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, endpoint, body)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// forward cookie for Core API SSO
	if v := c.Get("Cookie"); v != "" {
		req.Header.Set("Cookie", v)
	}
	// short timeout
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func nullIfEmptyJSON(j json.RawMessage) any {
	if len(j) == 0 || string(j) == "null" {
		return nil
	}
	return j
}
func nullIfEmptyStr(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}

func coalesceString(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

// simple string pointer to string helper for nullIfEmptyStr usage
func ptrString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// tiny string hasher for bucketing (not cryptographic)
func hash(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// helper: refund charge by gateway_ref for stripe
func stripeRefund(chargeID string) (any, error) {
	if strings.TrimSpace(chargeID) == "" {
		return nil, fmt.Errorf("missing charge id")
	}
	// Stripe refund by charge
	params := &stripe.RefundParams{Charge: stripe.String(chargeID)}
	r, err := refundNew(params)
	return r, err
}

// wrapped for testability
var refundNew = func(p *stripe.RefundParams) (*stripe.Refund, error) {
	return refund.New(p)
}

// uuidArrayLiteral converts a slice of UUID strings to a Postgres uuid[] literal
func uuidArrayLiteral(ids []string) string {
	if len(ids) == 0 {
		return "{}"
	}
	b := strings.Builder{}
	b.WriteByte('{')
	for i, s := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		// ensure quoted elements
		b.WriteByte('"')
		b.WriteString(s)
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

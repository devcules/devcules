// Package web serves the built PWA to browsers.
//
// It holds no data and answers no API: every file is public. The
// React source lives in web/; Vite builds it into internal/web/dist
// (gitignored except a placeholder, so Go builds without Node), and
// serve embeds the result. A missing index.html means the app was
// never built: every request then answers 503 with the build
// command, never a bare 404.
package web

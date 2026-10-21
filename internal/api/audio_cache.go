package api

// Revalidate on every use; shared proxies must never retain local music.
const audioCacheControl = "private, no-cache"

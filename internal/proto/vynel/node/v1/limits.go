package nodev1

// MaxMessage is the largest gRPC message the panel and the agent accept from each other.
// gRPC's default is 4 MB: a whole snapshot of a big node or a large stats batch may be more.
const MaxMessage = 64 << 20

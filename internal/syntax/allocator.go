package syntax

// go-tree-sitter's init() routes every tree-sitter malloc/free through
// exported Go shims (C -> Go callback -> cgo -> C.malloc), which costs
// ~30% of each keystroke's reparse in cgo callback overhead. The shims are
// plain proxies to libc malloc, so resetting the allocator to native libc
// is behavior-identical and keeps the perf gate honest. Our init runs after
// the binding's (imported packages initialize first).

/*
#include <stddef.h>
extern void ts_set_allocator(
	void *(*new_malloc)(size_t),
	void *(*new_calloc)(size_t, size_t),
	void *(*new_realloc)(void *, size_t),
	void (*new_free)(void *));
*/
import "C"

func init() {
	C.ts_set_allocator(nil, nil, nil, nil)
}

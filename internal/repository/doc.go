// Package repository declares the persistence interfaces the rest of kilasflow
// depends on, along with their GORM-backed implementations.
//
// Callers depend on the interfaces only. This is the seam that keeps GORM out
// of the engine and makes the storage backend replaceable.
//
// Milestone 1.
package repository

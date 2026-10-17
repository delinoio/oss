package domain

import "time"

// SubscriptionMemberState is the lifecycle of one execution member inside a
// shared account-owner lease.
type SubscriptionMemberState string

const (
	// SubscriptionMemberRunning is an admitted member that may still use the
	// account owner's native runtime.
	SubscriptionMemberRunning SubscriptionMemberState = "running"
	// SubscriptionMemberRetained is a member that reported completion or Stop
	// without proven native and descendant closure. It no longer runs, but its
	// workspace and cleanup ownership stay with the owner until final owner
	// containment closes. Retention is never a completed Stop.
	SubscriptionMemberRetained SubscriptionMemberState = "retained"
)

func (s SubscriptionMemberState) Valid() bool {
	return s == SubscriptionMemberRunning || s == SubscriptionMemberRetained
}

// SubscriptionFinishScope names the ownership one Finish releases.
type SubscriptionFinishScope string

const (
	// SubscriptionFinishLegacy keeps the historical singleton meaning: the one
	// lease and its credential generation settle together.
	SubscriptionFinishLegacy SubscriptionFinishScope = ""
	// SubscriptionFinishMember releases exactly one execution member and never
	// touches credentials, siblings or the owner.
	SubscriptionFinishMember SubscriptionFinishScope = "member"
	// SubscriptionFinishOwner retires the shared account owner once no runnable
	// member remains, with one generation-specific writeback.
	SubscriptionFinishOwner SubscriptionFinishScope = "owner"
)

// SubscriptionMember binds one session job to a shared owner lease. Every field
// is non-secret server-owned fencing metadata. Historical account JSON omits
// the extension and keeps its singleton representation.
type SubscriptionMember struct {
	// LeaseID is the identifier the Worker presents for this member. The founding
	// member shares the owner's lease identifier.
	LeaseID     ID                      `json:"lease_id"`
	OperationID ID                      `json:"operation_id"`
	Revision    uint64                  `json:"revision"`
	State       SubscriptionMemberState `json:"state"`
	AdmittedAt  time.Time               `json:"admitted_at"`
	// DirectStartup records that this member's claimed assignment is a
	// version-4 direct-startup execution, independent of its siblings.
	DirectStartup bool `json:"direct_startup,omitempty"`
}

// MaxSubscriptionMembers bounds the execution members one shared owner retains,
// in any state. Admission refuses a further member at this bound, so the complete
// member set always fits one recovery settlement request.
const MaxSubscriptionMembers = 256

// SharedOwner reports whether the lease uses the versioned account-owner plus
// execution-member representation.
func (s SubscriptionState) SharedOwner() bool {
	return s.Lease != nil && s.Lease.Shared
}

// RunnableMembers counts the members that may still use the owner's runtime.
func (s SubscriptionState) RunnableMembers() int {
	count := 0
	for _, member := range s.Members {
		if member.State == SubscriptionMemberRunning {
			count++
		}
	}
	return count
}

// Member returns the exact member with the given lease identifier.
func (s SubscriptionState) Member(lease ID) (SubscriptionMember, bool) {
	for _, member := range s.Members {
		if member.LeaseID == lease {
			return member, true
		}
	}
	return SubscriptionMember{}, false
}

// MemberByOperation returns the member that owns the exact session job.
func (s SubscriptionState) MemberByOperation(operation ID) (SubscriptionMember, bool) {
	for _, member := range s.Members {
		if member.OperationID == operation {
			return member, true
		}
	}
	return SubscriptionMember{}, false
}

// OwnerLeaseFor resolves a Worker-presented identifier for an owner-scoped
// operation. The owner's own identifier resolves to the stored lease (the
// founding member shares it). For a shared owner the identifier of another
// member also resolves, as that member's view, so the last runnable member can
// retire the owner.
func (s SubscriptionState) OwnerLeaseFor(lease ID) (SubscriptionLease, bool) {
	if s.Lease == nil {
		return SubscriptionLease{}, false
	}
	if s.Lease.ID == lease {
		return *s.Lease, true
	}
	if view, _, ok := s.MemberLease(lease); ok {
		return view, true
	}
	return SubscriptionLease{}, false
}

// MemberLease resolves one execution member of a shared owner. The returned view
// is the owner lease carrying the member's exact job and revision, so existing
// per-execution checks keep comparing the right job.
func (s SubscriptionState) MemberLease(lease ID) (SubscriptionLease, *SubscriptionMember, bool) {
	if s.Lease == nil || !s.Lease.Shared {
		return SubscriptionLease{}, nil, false
	}
	for i := range s.Members {
		if s.Members[i].LeaseID == lease {
			view := *s.Lease
			view.ID = s.Members[i].LeaseID
			view.OperationID = s.Members[i].OperationID
			view.Revision = s.Members[i].Revision
			view.Shared = false
			view.DirectStartup = s.Members[i].DirectStartup
			return view, &s.Members[i], true
		}
	}
	return SubscriptionLease{}, nil, false
}

// AdmitMember adds one runnable member to a shared owner.
func (s *SubscriptionState) AdmitMember(member SubscriptionMember) {
	s.Members = append(s.Members, member)
}

// ReleaseMember removes one member and reports whether it existed. The shared
// owner lease stays until its own retirement.
func (s *SubscriptionState) ReleaseMember(lease ID) bool {
	for i := range s.Members {
		if s.Members[i].LeaseID == lease {
			s.Members = append(s.Members[:i], s.Members[i+1:]...)
			if len(s.Members) == 0 {
				s.Members = nil
			}
			return true
		}
	}
	return false
}

// RetainMember marks one member as no longer runnable while keeping its
// workspace and cleanup ownership with the owner.
func (s *SubscriptionState) RetainMember(lease ID) bool {
	for i := range s.Members {
		if s.Members[i].LeaseID == lease {
			s.Members[i].State = SubscriptionMemberRetained
			return true
		}
	}
	return false
}

// validateOwnership checks the optional shared owner/member extension.
func (s SubscriptionState) validateOwnership() bool {
	if s.Lease == nil {
		return len(s.Members) == 0
	}
	if !s.Lease.Shared {
		return len(s.Members) == 0
	}
	l := s.Lease
	if l.Action != SubscriptionExecute || l.ForkRevision != 0 || l.TitleRevision != 0 {
		return false
	}
	leases := map[ID]bool{l.ID: true}
	operations := map[ID]bool{}
	for index, member := range s.Members {
		if member.LeaseID.Validate() != nil || member.OperationID.Validate() != nil || member.Revision == 0 || !member.State.Valid() || member.AdmittedAt.IsZero() || operations[member.OperationID] {
			return false
		}
		// Only the founding member shares the owner's identifier, and it is the
		// owner's own job at the owner's own revision. The same identifier
		// resolves to the stored owner through OwnerLeaseFor and to this member
		// through MemberLease, so the two views must never name different jobs.
		// Conversely, the owner's founding job belongs to the founding member
		// alone: no other member may carry it.
		if member.LeaseID == l.ID {
			if index != 0 || member.OperationID != l.OperationID || member.Revision != l.Revision {
				return false
			}
		} else if leases[member.LeaseID] || member.OperationID == l.OperationID {
			return false
		}
		leases[member.LeaseID] = true
		operations[member.OperationID] = true
	}
	return true
}

// LeaseForOperation resolves the lease that exactly owns one session job. A
// singleton lease owns its recorded job; a shared owner resolves only a running
// member, so a finished or retained member holds no execution authority.
func (s SubscriptionState) LeaseForOperation(operation ID) (SubscriptionLease, bool) {
	if s.Lease == nil {
		return SubscriptionLease{}, false
	}
	if !s.Lease.Shared {
		return *s.Lease, s.Lease.OperationID == operation
	}
	member, ok := s.MemberByOperation(operation)
	if !ok || member.State != SubscriptionMemberRunning {
		return SubscriptionLease{}, false
	}
	lease, _, ok := s.MemberLease(member.LeaseID)
	return lease, ok
}

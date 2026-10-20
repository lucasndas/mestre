package protocol

// Message types of the base protocol, used by every kind of connection.
const (
	TypeAck         = "ack"
	TypeError       = "error"
	TypeOutboxFloor = "outbox.floor"
)

// Message types between the motor of a Tower and the motor of the Citadel
// (docs/conectividade/mensagens.md).
const (
	TypeHello                = "hello"
	TypeWelcome              = "welcome"
	TypeMachineHeartbeat     = "machine.heartbeat"
	TypeMachineSheet         = "machine.sheet"
	TypeGuildEpoch           = "guild.epoch"
	TypeGuildConfig          = "guild.config"
	TypeGuildLeave           = "guild.leave"
	TypeGuildDiscarded       = "guild.discarded"
	TypeReplicaAck           = "replica.ack"
	TypeReplicaResync        = "replica.resync"
	TypeVaultReplica         = "vault.replica"
	TypeVaultRequest         = "vault.request"
	TypeVaultCredential      = "vault.credential"
	TypeVaultDrop            = "vault.drop"
	TypeVaultDropped         = "vault.dropped"
	TypeCitadelHandover      = "citadel.handover"
	TypeTerminalAdopt        = "terminal.adopt"
	TypeTerminalOpen         = "terminal.open"
	TypeTerminalOpened       = "terminal.opened"
	TypeTerminalInput        = "terminal.input"
	TypeTerminalClose        = "terminal.close"
	TypeTerminalStatus       = "terminal.status"
	TypeTerminalReplay       = "terminal.replay"
	TypeTerminalReplayed     = "terminal.replayed"
	TypeTerminalScreen       = "terminal.screen"
	TypeTerminalScreenResult = "terminal.screen_result"
	TypeInboxDeliver         = "inbox.deliver"
	TypeInboxResult          = "inbox.result"
	TypeCharacterStart       = "character.start"
	TypeCharacterStop        = "character.stop"
	TypeCharacterNotice      = "character.notice"
	TypeCharacterTurn        = "character.turn"
	TypeCharacterResync      = "character.resync"
	TypeTableWhere           = "table.where"
	TypeTableRemove          = "table.remove"
	TypeTableDone            = "table.done"
	TypeTableAttach          = "table.attach"
	TypeTableSent            = "table.sent"
	TypeTableNotice          = "table.notice"
	TypeRitualPreRun         = "ritual.prerun"
	TypeScrollPut            = "scroll.put"
	TypeScrollRemove         = "scroll.remove"
	TypeAgentRules           = "agent.rules"
	TypeAgentSiteLogin       = "agent.site_login"
	TypeAgentSiteLoginStatus = "agent.site_login_status"
	TypeAgentSiteLoginInput  = "agent.site_login_input"
	TypeOperatorSession      = "operator.session"
	TypeOperatorCapture      = "operator.capture"
	TypeAgentEvent           = "agent.event"
	TypeUsageReading         = "usage.reading"
	TypeCLICall              = "cli.call"
	TypeCLIResult            = "cli.result"
	TypeSyncRequest          = "sync.request"
	TypeSyncApplied          = "sync.applied"
	TypeXferBegin            = "xfer.begin"
	TypeXferReady            = "xfer.ready"
	TypeXferProgress         = "xfer.progress"
	TypeXferEnd              = "xfer.end"
	TypeXferDone             = "xfer.done"
	TypeTranscribeRequest    = "transcribe.request"
	TypeTranscribeResult     = "transcribe.result"
	TypeCertRenew            = "cert.renew"
	TypeCertIssued           = "cert.issued"
	TypeUpdateApply          = "update.apply"
)

// BaseRegistry returns a registry with `ack`, `error` and `outbox.floor`,
// which every connection speaks; the Guardian connection adds its own types to it.
func BaseRegistry() *Registry {
	r := NewRegistry()
	mustAdd(r,
		Spec{Type: TypeAck, Dir: Both, Payload: Ack{}},
		Spec{Type: TypeError, Dir: Both, Payload: Error{}},
		Spec{Type: TypeOutboxFloor, Dir: Both, Payload: OutboxFloor{}},
	)
	return r
}

// DefaultRegistry returns the catalogue of the motor-to-motor connection:
// one entry per row of the table of docs/conectividade/mensagens.md, plus
// the answers that the table names. The payload structs are the source of the
// fields. Every call returns a registry of its own, free to be extended.
func DefaultRegistry() *Registry {
	r := BaseRegistry()
	mustAdd(r,
		Spec{Type: TypeHello, Dir: TowerToCitadel, Payload: Hello{}},
		Spec{Type: TypeWelcome, Dir: CitadelToTower, Payload: Welcome{}},
		Spec{Type: TypeMachineHeartbeat, Dir: TowerToCitadel, Payload: MachineHeartbeat{}},
		Spec{Type: TypeMachineSheet, Dir: TowerToCitadel, Ack: true, Payload: Object{}},
		Spec{Type: TypeGuildEpoch, Dir: Both, Payload: Empty{}, Reply: Certificate{}},
		Spec{Type: TypeGuildConfig, Dir: CitadelToTower, Ack: true, Payload: GuildConfig{}},
		Spec{Type: TypeGuildLeave, Dir: Both, Ack: true, Payload: GuildLeave{}},
		Spec{Type: TypeGuildDiscarded, Dir: TowerToCitadel, Ack: true, Payload: GuildDiscarded{}},
		Spec{Type: TypeReplicaAck, Dir: TowerToCitadel, Payload: ReplicaAck{}},
		Spec{Type: TypeReplicaResync, Dir: TowerToCitadel, Ack: true, Payload: ReplicaResync{}},
		Spec{Type: TypeVaultReplica, Dir: CitadelToTower, Ack: true, Volatile: true, Payload: VaultReplica{}},
		Spec{Type: TypeVaultRequest, Dir: TowerToCitadel, Ack: true, Payload: VaultRequest{}},
		Spec{Type: TypeVaultCredential, Dir: CitadelToTower, Ack: true, Volatile: true, Payload: VaultCredential{}},
		Spec{Type: TypeVaultDrop, Dir: CitadelToTower, Ack: true, Payload: Empty{}},
		Spec{Type: TypeVaultDropped, Dir: TowerToCitadel, Ack: true, Payload: Empty{}},
		Spec{Type: TypeCitadelHandover, Dir: CitadelToTower, Ack: true, Payload: CitadelHandover{}},
		Spec{Type: TypeTerminalAdopt, Dir: TowerToCitadel, Ack: true, Payload: TerminalAdopt{}},
		Spec{Type: TypeTerminalOpen, Dir: CitadelToTower, Ack: true, Payload: TerminalOpen{}},
		Spec{Type: TypeTerminalOpened, Dir: TowerToCitadel, Ack: true, Payload: TerminalOpened{}},
		Spec{Type: TypeTerminalInput, Dir: CitadelToTower, Ack: true, Payload: TerminalInput{}},
		Spec{Type: TypeTerminalClose, Dir: CitadelToTower, Ack: true, Payload: TerminalClose{}},
		Spec{Type: TypeTerminalStatus, Dir: TowerToCitadel, Ack: true, Payload: TerminalStatus{}},
		Spec{Type: TypeTerminalReplay, Dir: CitadelToTower, Payload: TerminalReplay{}},
		Spec{Type: TypeTerminalReplayed, Dir: TowerToCitadel, Payload: TerminalReplayed{}},
		Spec{Type: TypeTerminalScreen, Dir: CitadelToTower, Ack: true, Payload: TerminalScreen{}},
		Spec{Type: TypeTerminalScreenResult, Dir: TowerToCitadel, Payload: TerminalScreenResult{}},
		Spec{Type: TypeInboxDeliver, Dir: CitadelToTower, Ack: true, Payload: InboxDeliver{}},
		Spec{Type: TypeInboxResult, Dir: TowerToCitadel, Ack: true, Payload: InboxResult{}},
		Spec{Type: TypeCharacterStart, Dir: CitadelToTower, Ack: true, Volatile: true, Payload: CharacterStart{}},
		Spec{Type: TypeCharacterStop, Dir: CitadelToTower, Ack: true, Payload: CharacterStop{}},
		Spec{Type: TypeCharacterNotice, Dir: TowerToCitadel, Ack: true, Payload: CharacterNotice{}},
		Spec{Type: TypeCharacterTurn, Dir: TowerToCitadel, Ack: true, Payload: CharacterTurn{}},
		Spec{Type: TypeCharacterResync, Dir: TowerToCitadel, Ack: true, Payload: CharacterResync{}, Reply: CharacterResyncResult{}},
		Spec{Type: TypeTableWhere, Dir: CitadelToTower, Ack: true, Payload: TableRef{}, Reply: TableWhere{}},
		Spec{Type: TypeTableRemove, Dir: CitadelToTower, Ack: true, Payload: TableRef{}},
		Spec{Type: TypeTableDone, Dir: CitadelToTower, Ack: true, Payload: TableRef{}},
		Spec{Type: TypeTableAttach, Dir: CitadelToTower, Ack: true, Payload: TableAttach{}},
		Spec{Type: TypeTableSent, Dir: TowerToCitadel, Ack: true, Payload: TableSent{}},
		Spec{Type: TypeTableNotice, Dir: TowerToCitadel, Ack: true, Payload: TableNotice{}},
		Spec{Type: TypeRitualPreRun, Dir: CitadelToTower, Ack: true, Payload: RitualPreRun{}, Reply: RitualPreRunResult{}},
		Spec{Type: TypeScrollPut, Dir: CitadelToTower, Ack: true, Payload: ScrollPut{}},
		Spec{Type: TypeScrollRemove, Dir: CitadelToTower, Ack: true, Payload: ScrollRemove{}},
		Spec{Type: TypeAgentRules, Dir: CitadelToTower, Ack: true, Payload: Object{}},
		Spec{Type: TypeAgentSiteLogin, Dir: CitadelToTower, Ack: true, Volatile: true, Payload: AgentSiteLogin{}},
		Spec{Type: TypeAgentSiteLoginStatus, Dir: TowerToCitadel, Ack: true, Payload: AgentSiteLoginStatus{}},
		Spec{Type: TypeAgentSiteLoginInput, Dir: CitadelToTower, Ack: true, Volatile: true, Payload: AgentSiteLoginInput{}},
		Spec{Type: TypeOperatorSession, Dir: TowerToCitadel, Ack: true, Payload: OperatorSession{}},
		Spec{Type: TypeOperatorCapture, Dir: TowerToCitadel, Ack: true, Payload: OperatorCapture{}},
		Spec{Type: TypeAgentEvent, Dir: TowerToCitadel, Ack: true, Payload: Object{}},
		Spec{Type: TypeUsageReading, Dir: TowerToCitadel, Ack: true, Payload: UsageReading{}},
		Spec{Type: TypeCLICall, Dir: TowerToCitadel, Ack: true, Payload: CLICall{}},
		Spec{Type: TypeCLIResult, Dir: CitadelToTower, Ack: true, Payload: CLIResult{}},
		Spec{Type: TypeSyncRequest, Dir: Both, Ack: true, Payload: SyncMessage{}},
		Spec{Type: TypeSyncApplied, Dir: Both, Ack: true, Payload: SyncMessage{}},
		Spec{Type: TypeXferBegin, Dir: Both, Ack: true, Payload: XferBegin{}},
		Spec{Type: TypeXferReady, Dir: Both, Ack: true, Payload: XferReady{}},
		Spec{Type: TypeXferProgress, Dir: Both, Payload: XferProgress{}},
		Spec{Type: TypeXferEnd, Dir: Both, Ack: true, Payload: XferEnd{}},
		Spec{Type: TypeXferDone, Dir: Both, Ack: true, Payload: XferDone{}},
		Spec{Type: TypeTranscribeRequest, Dir: CitadelToTower, Ack: true, Payload: TranscribeRequest{}},
		Spec{Type: TypeTranscribeResult, Dir: TowerToCitadel, Ack: true, Payload: TranscribeResult{}},
		Spec{Type: TypeCertRenew, Dir: TowerToCitadel, Ack: true, Payload: CertRenew{}},
		Spec{Type: TypeCertIssued, Dir: CitadelToTower, Ack: true, Payload: CertIssued{}},
		Spec{Type: TypeUpdateApply, Dir: CitadelToTower, Ack: true, Payload: UpdateApply{}},
	)
	return r
}

func mustAdd(r *Registry, specs ...Spec) {
	for _, s := range specs {
		if err := r.Add(s); err != nil {
			panic(err)
		}
	}
}

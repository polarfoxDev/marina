# Marina Dynamic Discovery Architecture

## System Diagram

```text
┌─────────────────────────────────────────────────────────────┐
│                    Marina Backup Manager                    │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐    │
│  │              Initialization                         │    │
│  │  • Load config.yml                                  │    │
│  │  • Initialize Restic repositories                   │    │
│  │  • Create Runner with cron scheduler                │    │
│  └─────────────────────────────────────────────────────┘    │
│                           │                                 │
│                           ▼                                 │
│  ┌─────────────────────────────────────────────────────┐    │
│  │         Initial Discovery (Docker API)              │    │
│  │  • Verify configured volumes exist                  │    │
│  │  • Verify configured containers exist               │    │
│  │  • Build BackupTarget list from config.yml          │    │
│  └─────────────────────────────────────────────────────┘    │
│                           │                                 │
│                           ▼                                 │
│  ┌─────────────────────────────────────────────────────┐    │
│  │          Runner.SyncTargets()                       │    │
│  │  • Add new targets → ScheduleTarget()               │    │
│  │  • Remove deleted targets → RemoveTarget()          │    │
│  │  • Update changed targets → Reschedule              │    │
│  └─────────────────────────────────────────────────────┘    │
│                           │                                 │
│           ┌───────────────┴───────────────┐                 │
│           ▼                               ▼                 │
│  ┌─────────────────┐           ┌──────────────────┐         │
│  │ Event Listener  │           │ Periodic Poller  │         │
│  │  (Real-time)    │           │   (Fallback)     │         │
│  ├─────────────────┤           ├──────────────────┤         │
│  │ • Docker Events │           │ • Timer: 30s     │         │
│  │ • Debounce: 2s  │           │ • Full scan      │         │
│  │ • Auto-reconnect│           │ • Reliable       │         │
│  └────────┬────────┘           └────────┬─────────┘         │
│           │                             │                   │
│           └──────────┬──────────────────┘                   │
│                      │ Trigger                              │
│                      ▼                                      │
│           ┌──────────────────────┐                          │
│           │ Rediscovery Callback │                          │
│           │  • Scan Docker again │                          │
│           │  • Call SyncTargets()│                          │
│           └──────────────────────┘                          │
│                      │                                      │
│                      ▼                                      │
│  ┌─────────────────────────────────────────────────────┐    │
│  │            Cron Scheduler (robfig/cron)             │    │
│  │                                                     │    │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐           │    │
│  │  │ Job 1    │  │ Job 2    │  │ Job N    │           │    │
│  │  │ volume:x │  │ db:y     │  │ ...      │           │    │
│  │  │ */5****  │  │ 0 2 ***  │  │          │           │    │
│  │  └────┬─────┘  └────┬─────┘  └────┬─────┘           │    │
│  └───────┼─────────────┼─────────────┼─────────────────┘    │
│          │             │             │                      │
└──────────┼─────────────┼─────────────┼──────────────────────┘
           │             │             │
           ▼             ▼             ▼
    ┌──────────────────────────────────────┐
    │      Runner.runOnce(target)          │
    │                                      │
    │  Volume Backup:        DB Backup:    │
    │  • Pre-hook           • Pre-hook     │
    │  • Stop containers    • Dump DB      │
    │  • Backup paths       • Copy to host │
    │  • Start containers   • Backup dump  │
    │  • Post-hook          • Post-hook    │
    │  • Apply retention    • Retention    │
    └───────────┬──────────────────────────┘
                │
                ▼
         ┌─────────────┐
         │   Restic    │
         │  Backend    │
         │             │
         │ • backup    │
         │ • forget    │
         │ • prune     │
         └─────────────┘
```

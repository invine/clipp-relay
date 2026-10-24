Client establishes connection
Client starts auth protocol
Relay handles Auth protocol by calling GetAccountByTokenQuery and GetQuotaByAccountQuery
Account and Quota are stored in memory. Who's the owner of account and quota? Quota is owned by Account since it can be changed by changing account plan, Account is owned by the portal as admin can change the account status. So there's no need to store quota or account in the relay as relay doesn't own them
Question is should we have upfront reservation of the capacity or it can be done in the background. This is the main question right now

Let's assume that reservation should be done upfront, then relay will call ReserveQuotaCommand. Who should decide what's the size of the reservation? If we continue with the current approach, relay is deciding the size. I think it's correct as Quota should not decide how much is reserved, it's decided by data plane.

In this scenario relay calls ReserveQuotaCommand to reserve let's say 64 KiB, stores in memory that connection has 64 KiB to consume. ReserveQuotaCommand in turn commits to DB that client reserved capacity. In this situation, we don't even need to consume, reservation never returned.

Issue with this approach is that client need to reserve slots all the time upfront. It will create a bottleneck upfront for the clients.

The other approach is to check only if there's positive balance and commit usage after it happened and update balance asynchronously. In this case, account can use a little bit more that allowed sometimes but there's no bottleneck upfront. All the usage and balance update is happening at the predictable intervals and quota will not limit usage.
In this situation relay keeps current remaining balance of the connection in memory, it uses GetAccountBalanceQuery and CommitUsageCommand periodically to understand how much is left.
In this case I don't need to interact with relay from other use cases specifically. It will eventually close the connection for expired token, blocked account, exhausted balance, etc.
Relay can do the check once every second. This was increasing bandwidth will not increase load on the quota service.

Interface in this case will be:
GetAccountBalanceQuery, CommitUsageCommand, GetAccountByTokenQuery provided to relay.


How should do we deal with grant renewal? What should relay know about the account? If it's constantly querying the balance, it should pass the current grant, not just the account, right or wrong? Should there be another layer which says if connection is authenticated or not? To avoid the concerns about authorization to leak into quota service.

Relay should provide DetachAccountCommand for Account service which will allow account service to terminate connection if grant is revoked or if account is blocked by admin.

I should disallow any streams on relay except for auth protocol stream without authentication.

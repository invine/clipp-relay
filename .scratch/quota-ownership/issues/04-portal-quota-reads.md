# 04: Serve Portal usage and live counts outside Auth

**What to build:** Owner and administrator pages show their existing usage and capacity information through the Portal read path. Auth supplies authorization and active Login Grant facts, while Quota supplies the live count.

**Blocked by:** [02: Move final admission and live counts into Quota](02-quota-admission-live-counts.md)

**Status:** claimed

**Claimed by:** quota-ownership-coordinator (2026-11-05)

- [ ] Route Recorded Relay Usage, remaining allowance and history directly to the Portal read path. Auth's authorization results contain credential or policy facts without usage totals or live counts.
- [ ] With authenticated owner and administrator routes backed by disposable PostgreSQL, verify configured caps, confirmed current-week usage and history, and the owner capacity distinction between live Relay Sessions and active Login Grants. Exercise known zero and unavailable count separately.
- [ ] Revoke the owner Portal Session between initial authorization and the protected profile read. Assert that the response exposes no usage data. Keep the session recheck and durable policy-and-usage snapshot within one database operation.
- [ ] Preserve administrator authority and deleted-account visibility, existing responses and rendering. Keep live count sampling separate from the durable profile snapshot.
- [ ] Retire the previous Auth usage projection and count sampler for the migrated pages. Existing Portal behavior tests continue to pass.

**Spec coverage:** I1, I5–I6, I8 · T8. See the [parent specification](../spec.md) and [decision record](../decisions.md).

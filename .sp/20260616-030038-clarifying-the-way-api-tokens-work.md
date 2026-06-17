---
title: "clarifying the way api tokens work"
created_at: "2026-06-16T03:00:38Z"
slug: "clarifying-the-way-api-tokens-work"
copied_at: "2026-06-17T08:35:56Z"
---

# clarifying the way api tokens work

i want to make sure api tokens are working in the expected manner, here is what I assume. Imagine I have a system, and i log onto that system. on that system I can run cfasuite set-api-token some-api-token and that sets a particular env on the system. This can also be done manually, but the cli makes setting env variables trivial on all systems. Okay, from there, we can ensure cfasuite-hr is running, and then as long and the ENV variable is set to a valid api token in the cfasuite-hr system, other applications can connect via that single env variable. That env variable is what is checked when we are asking the question: "is this application allowed to access cfasuite-hr data?"

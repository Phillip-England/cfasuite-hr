---
title: "creating a simple migration script that deletes itself after use"
created_at: "2026-06-15T21:05:58Z"
slug: "creating-a-simple-migration-script-that-deletes-itself-after-use"
copied_at: "2026-06-17T08:35:56Z"
---

# creating a simple migration script that deletes itself after use

okay because we are changing the database here's the change that we are going to be making right now in my production database my locations exist for all locations sorry my rolls and departments my roles and departments exist for all locations and so I have employees that are assigned to these roles and departments but we're getting ready to make it so that rolls and departments are location specific so here is what the migration should do the migration should take my sqlite database and it should take the roles and departments that are currently created and then it should create those roles within each location and then it should delete the main databases for the roles and departments and essentially sync up my production database with the new version of the SQL light database that would be required to run the application after the changes made this should be done in such a way that I don't have to go back and reapply my employees to all the roles jobs and departments they currently have they should be done in such a way that it stays consistent exactly what you would expect from a migration going from one structure to another

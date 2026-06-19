# Architecture

Quack keeps these top-level layers separate so publishing, public traffic, and future runtime execution can evolve independently.

## Transport

HTTP adapters live at the edges of the system. The admin/control API handles deployment and management routes. The public HTTP surface handles live site traffic. UI handlers render the admin experience.

Transport packages should translate HTTP requests and responses, then delegate product decisions to application services.

## Application Services

Application services orchestrate use cases such as upload, publish, unpublish, rollback, delete, settings updates, policy reconciliation, and current-site reads.

Services own workflow rules and depend on repositories or lower-level infrastructure interfaces rather than concrete transport handlers.

## Domain Concepts

Domain packages define stable product concepts such as sites, releases, policy records, upload records, files, users, settings, and serving status.

Domain concepts should avoid importing HTTP, SQLite, filesystem storage, or UI packages.

## Infrastructure

Infrastructure packages implement persistence, blob storage, and caching. They should make reads and writes cheap and reliable without becoming the owner of product workflow decisions.

## Composition Root

`internal/server` wires concrete infrastructure, application services, and transport handlers together. Package construction and route registration belong here.

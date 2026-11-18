# dictyBase Cluster Operations

The services, data, and people that the cluster operations in this repository
serve. Terms here describe the domain, not the tooling.

## File exchange

**Collaborator**:
A person outside the project who sends files to the project. A collaborator
has no account.
_Avoid_: external user, guest, uploader

**Staff**:
A project member who reviews uploads, publishes datasets, and manages links.
_Avoid_: admin, operator (as a role name)

**Upload link**:
A private link that lets one collaborator, after an email check, add files to
the collaborator's own folder. It cannot show or read files.
_Avoid_: upload share, drop link, intake link

**Upload**:
A file that a collaborator sent and that staff did not review yet.
_Avoid_: submission, incoming file

**Dataset**:
A named group of reviewed files that the project makes public together.
_Avoid_: collection, release

**Publication**:
The act of making reviewed uploads part of a dataset. A published file never
changes its content.
_Avoid_: release, export

**Published file**:
A file in a dataset. Any person can read it.
_Avoid_: public file, download

**Public link**:
The one read-only link where any person can browse every dataset.
_Avoid_: public share, browse link

**Direct file URL**:
The stable address of one published file, for citation and for scripts.
_Avoid_: download link, permalink

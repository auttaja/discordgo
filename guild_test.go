package discordgo

import "testing"

func getGuild(t *testing.T) (g *Guild) {
	if envGuild == "" {
		t.Skip("Skipping, DG_GUILD not set.")
	}

	if dg == nil {
		t.Skip("Skipping, dg not set.")
	}

	g, err := dg.State.Guild(envGuild)
	if err != nil {
		t.Fatalf("Guild not found, id: %s; %s", envGuild, err)
	}

	if g.Unavailable {
		t.Fatalf("Guild %s is still unavailable", envGuild)
	}
	return
}

func TestGuild_GetChannel(t *testing.T) {
	g := getGuild(t)

	_, err := g.GetChannel(envChannel)
	if err != nil {
		t.Fatalf("Channel not found in guild")
	}
}

func TestGuild_GetRole(t *testing.T) {
	g := getGuild(t)

	_, err := g.GetRole(envRole)
	if err != nil {
		t.Fatalf("Role not found in guild")
	}
}

func TestGuild_CreateDeleteRole(t *testing.T) {
	g := getGuild(t)

	r, err := g.CreateRole()
	if err != nil {
		t.Fatalf("Role failed to create in Guild; %s", err)
	}

	editData := &RoleEdit{
		Name:        "OwO a testing role",
		Hoist:       false,
		Color:       ColorGreen,
		Permissions: r.Permissions,
		Mentionable: true,
	}

	r, err = r.EditComplex(editData)
	if err != nil {
		t.Fatalf("Failed at editing role; %s", err)
	}

	err = r.Delete()
	if err != nil {
		t.Fatalf("Failed at deleteing role; %s", err)
	}
}

func TestRole_Move(t *testing.T) {
	g := getGuild(t)

	r, err := g.CreateRole()
	if err != nil {
		t.Fatalf("Role failed to create in Guild; %s", err)
	}

	c := ColorGreen
	err = c.SetHex("#ffff00")
	if err != nil {
		t.Fatalf("failed at parsing hex code; %s", err)
	}

	editData := &RoleEdit{
		Name:        "OwO a moving role",
		Hoist:       false,
		Color:       c,
		Permissions: r.Permissions,
		Mentionable: false,
	}

	r, err = r.EditComplex(editData)
	if err != nil {
		t.Fatalf("Failed at editing role; %s", err)
	}

	err = r.Move(6)
	if err != nil {
		t.Fatalf("failed at moving role; %s", err)
	}
}
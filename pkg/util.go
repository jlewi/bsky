package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	cliutil "github.com/bluesky-social/indigo/util/cliutil"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/fatih/color"
	cidDecode "github.com/ipfs/go-cid"

	"github.com/urfave/cli/v2"
)

func PrintPost(p *bsky.FeedDefs_PostView) {
	rec := p.Record.Val.(*bsky.FeedPost)
	color.Set(color.FgHiRed)
	fmt.Print(p.Author.Handle)
	color.Set(color.Reset)
	fmt.Printf(" [%s]", Stringp(p.Author.DisplayName))
	fmt.Printf(" (%s)\n", Timep(rec.CreatedAt).Format(time.RFC3339))
	if rec.Entities != nil {
		sort.Slice(rec.Entities, func(i, j int) bool {
			return rec.Entities[i].Index.Start < rec.Entities[j].Index.Start
		})
		rs := []rune(rec.Text)
		off := int64(0)
		for _, e := range rec.Entities {
			if e.Index.Start < 0 {
				e.Index.Start = 0
			}
			if e.Index.End > int64(len(rs)) {
				e.Index.End = int64(len(rs))
			}
			fmt.Print(string(rs[off:e.Index.Start]))
			if e.Type == "mention" {
				color.Set(color.Bold)
			} else {
				color.Set(color.Underline)
			}
			fmt.Print(string(rs[e.Index.Start:e.Index.End]))
			color.Set(color.Reset)
			off = e.Index.End
		}
		if off < int64(len(rs)) {
			fmt.Print(string(rs[off:]))
		}
		fmt.Println()
		//for _, e := range rec.Entities {
		//	fmt.Printf(" {%s}\n", e.Value)
		//}
	} else {
		fmt.Println(rec.Text)
	}
	if p.Embed != nil {
		if p.Embed.EmbedImages_View != nil {
			for _, i := range p.Embed.EmbedImages_View.Images {
				fmt.Println(" {" + i.Fullsize + "}")
			}
		}
	}
	fmt.Printf(" 👍(%d)⚡(%d)↩️ (%d)\n",
		Int64p(p.LikeCount),
		Int64p(p.RepostCount),
		Int64p(p.ReplyCount),
	)
	if rec.Reply != nil && rec.Reply.Parent != nil {
		fmt.Print(" > ")
		color.Set(color.FgBlue)
		fmt.Println(rec.Reply.Parent.Uri)
		color.Set(color.Reset)
	}
	fmt.Print(" - ")
	color.Set(color.FgBlue)
	fmt.Println(p.Uri)
	color.Set(color.Reset)
	fmt.Println()
}

var formats = []string{
	"2006-01-02T15:04:05",
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05.000Z",
	"2006-01-02T15:04:05.000000Z",
	"2006-01-02T15:04:05-07:00",
}

func Timep(s string) time.Time {
	for _, f := range formats {
		t, err := time.Parse(f, s)
		if err == nil {
			return t.Local()
		}
	}
	panic(s)
}

func Int64p(i *int64) int64 {
	if i == nil {
		return 0
	}
	return *i
}

func Stringp(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func MakeXRPCC(cCtx *cli.Context) (*xrpc.Client, error) {
	cfg := cCtx.App.Metadata["Config"].(*Config)

	xrpcc := &xrpc.Client{
		Client: cliutil.NewHttpClient(),
		Host:   cfg.Host,
		Auth:   &xrpc.AuthInfo{Handle: cfg.Handle},
	}

	auth, err := cliutil.ReadAuth(filepath.Join(cfg.dir, cfg.Prefix+cfg.Handle+".auth"))
	if err == nil {
		xrpcc.Auth = auth
		xrpcc.Auth.AccessJwt = xrpcc.Auth.RefreshJwt
		refresh, err2 := comatproto.ServerRefreshSession(context.TODO(), xrpcc)
		if err2 != nil {
			err = err2
		} else {
			xrpcc.Auth.Did = refresh.Did
			xrpcc.Auth.AccessJwt = refresh.AccessJwt
			xrpcc.Auth.RefreshJwt = refresh.RefreshJwt

			b, err := json.Marshal(xrpcc.Auth)
			if err == nil {
				if err := os.WriteFile(filepath.Join(cfg.dir, cfg.Prefix+cfg.Handle+".auth"), b, 0600); err != nil {
					return nil, fmt.Errorf("cannot write auth file: %w", err)
				}
			}
		}
	}
	if err != nil {
		auth, err := comatproto.ServerCreateSession(context.TODO(), xrpcc, &comatproto.ServerCreateSession_Input{
			Identifier: xrpcc.Auth.Handle,
			Password:   cfg.Password,
		})
		if err != nil {
			return nil, fmt.Errorf("cannot create session: %w", err)
		}
		xrpcc.Auth.Did = auth.Did
		xrpcc.Auth.AccessJwt = auth.AccessJwt
		xrpcc.Auth.RefreshJwt = auth.RefreshJwt

		b, err := json.MarshalIndent(xrpcc.Auth, "", "  ")
		if err == nil {
			if err := os.WriteFile(filepath.Join(cfg.dir, cfg.Prefix+cfg.Handle+".auth"), b, 0600); err != nil {
				return nil, fmt.Errorf("cannot write auth file: %w", err)
			}
		}
	}

	return xrpcc, nil
}

var avatarOrBannerUrlRegex = regexp.MustCompile(`^https://cdn\.bsky\.app/img/(avatar|banner)/plain/did:plc:[a-z0-9]+/[a-z0-9]+@+[a-z]+$`)

func ParseCid(cidUrl *string) (cidDecode.Cid, string, error) {
	if cidUrl == nil {
		return cidDecode.Cid{}, "", fmt.Errorf("URL is not provided")
	}

	if !avatarOrBannerUrlRegex.MatchString(*cidUrl) {
		return cidDecode.Cid{}, "", fmt.Errorf("URL does not match expected format")
	}

	parsedCidUrl, err := url.Parse(*cidUrl)
	if err != nil {
		return cidDecode.Cid{}, "", fmt.Errorf("failed to parse URL: %w", err)
	}

	pathSegments := strings.Split(parsedCidUrl.Path, "/")
	cid := pathSegments[len(pathSegments)-1]

	cidParts := strings.Split(cid, "@")
	if len(cidParts) < 2 {
		return cidDecode.Cid{}, "", fmt.Errorf("CID does not contain image type")
	}

	cid, imageType := cidParts[0], cidParts[1]
	decodedCid, err := cidDecode.Decode(cid)
	if err != nil {
		return cidDecode.Cid{}, "", fmt.Errorf("failed to decode CID: %w", err)
	}

	return decodedCid, "image/" + imageType, nil
}
